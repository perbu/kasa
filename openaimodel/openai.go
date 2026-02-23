// Package openaimodel adapts the OpenAI chat completions API to ADK's model.LLM interface.
// Based on github.com/byebyebruce/adk-go-openai, vendored and modified for:
//   - Lowercase genai.Type values ("object" vs "OBJECT") used by kasa tools
//   - Proper schema conversion via convertSchema() in convertTools()
//   - StreamOptions for token usage reporting
//   - OpenRouter HTTP headers
package openaimodel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/sashabaranov/go-openai"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

var _ model.LLM = &Model{}

var (
	ErrNoChoicesInResponse = errors.New("no choices in OpenAI response")
)

// Model implements the ADK model.LLM interface using an OpenAI-compatible API.
type Model struct {
	Client    *openai.Client
	ModelName string
}

// New creates a Model for the given OpenAI-compatible endpoint.
// baseURL should be e.g. "https://openrouter.ai/api/v1".
// transport is an optional http.RoundTripper (for retries, etc); nil uses http.DefaultTransport.
func New(modelName, apiKey, baseURL string, transport http.RoundTripper) *Model {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL

	if transport == nil {
		transport = http.DefaultTransport
	}

	// Wrap with OpenRouter headers.
	cfg.HTTPClient = &http.Client{
		Transport: &openRouterTransport{base: transport},
	}

	client := openai.NewClientWithConfig(cfg)
	return &Model{
		Client:    client,
		ModelName: modelName,
	}
}

// Name implements model.LLM.
func (o *Model) Name() string {
	return o.ModelName
}

// GenerateContent implements model.LLM.
func (o *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if stream {
		return o.generateStream(ctx, req)
	}
	return o.generate(ctx, req)
}

func (o *Model) generate(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		openaiReq, err := toOpenAIChatCompletionRequest(req, o.ModelName)
		if err != nil {
			yield(nil, err)
			return
		}

		resp, err := o.Client.CreateChatCompletion(ctx, openaiReq)
		if err != nil {
			yield(nil, err)
			return
		}

		llmResp, err := convertChatCompletionResponse(&resp)
		if err != nil {
			yield(nil, err)
			return
		}

		yield(llmResp, nil)
	}
}

func (o *Model) generateStream(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		openaiReq, err := toOpenAIChatCompletionRequest(req, o.ModelName)
		if err != nil {
			yield(nil, err)
			return
		}
		openaiReq.Stream = true
		openaiReq.StreamOptions = &openai.StreamOptions{
			IncludeUsage: true,
		}

		stream, err := o.Client.CreateChatCompletionStream(ctx, openaiReq)
		if err != nil {
			yield(nil, err)
			return
		}
		defer stream.Close()

		aggregatedContent := &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{},
		}
		var finishReason genai.FinishReason
		var usageMetadata *genai.GenerateContentResponseUsageMetadata

		toolCallsMap := make(map[int]*toolCallBuilder)

		lastPartIsText := false
		for {
			chunk, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				yield(nil, err)
				return
			}

			// Usage-only chunk (no choices) — capture and continue.
			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usageMetadata = &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:     int32(chunk.Usage.PromptTokens),
						CandidatesTokenCount: int32(chunk.Usage.CompletionTokens),
						TotalTokenCount:      int32(chunk.Usage.TotalTokens),
					}
				}
				continue
			}

			choice := chunk.Choices[0]

			if choice.Delta.Content != "" {
				part := &genai.Part{Text: choice.Delta.Content}
				if lastPartIsText {
					aggregatedContent.Parts[len(aggregatedContent.Parts)-1].Text += part.Text
				} else {
					aggregatedContent.Parts = append(aggregatedContent.Parts, part)
				}

				lastPartIsText = true
				llmResp := &model.LLMResponse{
					Content:      &genai.Content{Role: "model", Parts: []*genai.Part{part}},
					Partial:      true,
					TurnComplete: false,
				}
				if !yield(llmResp, nil) {
					return
				}
			} else {
				lastPartIsText = false
			}

			if len(choice.Delta.ToolCalls) > 0 {
				for _, toolCall := range choice.Delta.ToolCalls {
					idx := 0
					if toolCall.Index != nil {
						idx = *toolCall.Index
					}

					builder, exists := toolCallsMap[idx]
					if !exists {
						builder = &toolCallBuilder{
							id:   toolCall.ID,
							name: toolCall.Function.Name,
							args: "",
						}
						toolCallsMap[idx] = builder
					}

					if toolCall.ID != "" {
						builder.id = toolCall.ID
					}
					if toolCall.Function.Name != "" {
						builder.name = toolCall.Function.Name
					}
					if toolCall.Function.Arguments != "" {
						builder.args += toolCall.Function.Arguments
					}
				}
			}

			if choice.FinishReason != "" {
				finishReason = convertFinishReason(string(choice.FinishReason))
			}

			if chunk.Usage != nil {
				usageMetadata = &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     int32(chunk.Usage.PromptTokens),
					CandidatesTokenCount: int32(chunk.Usage.CompletionTokens),
					TotalTokenCount:      int32(chunk.Usage.TotalTokens),
				}
			}
		}

		// Convert aggregated tool calls to parts.
		if len(toolCallsMap) > 0 {
			indices := make([]int, 0, len(toolCallsMap))
			for idx := range toolCallsMap {
				indices = append(indices, idx)
			}
			for i := 0; i < len(indices)-1; i++ {
				for j := 0; j < len(indices)-i-1; j++ {
					if indices[j] > indices[j+1] {
						indices[j], indices[j+1] = indices[j+1], indices[j]
					}
				}
			}

			for _, idx := range indices {
				builder := toolCallsMap[idx]
				part := &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   builder.id,
						Name: builder.name,
						Args: parseJSONArgs(builder.args),
					},
				}
				aggregatedContent.Parts = append(aggregatedContent.Parts, part)
			}
		}

		finalResp := &model.LLMResponse{
			Content:       aggregatedContent,
			UsageMetadata: usageMetadata,
			FinishReason:  finishReason,
			Partial:       false,
			TurnComplete:  true,
		}
		yield(finalResp, nil)
	}
}

type toolCallBuilder struct {
	id   string
	name string
	args string
}

func toOpenAIChatCompletionRequest(req *model.LLMRequest, modelName string) (openai.ChatCompletionRequest, error) {
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(req.Contents))
	for _, content := range req.Contents {
		msgs, err := toOpenAIChatCompletionMessage(content)
		if err != nil {
			return openai.ChatCompletionRequest{}, err
		}
		openaiMessages = append(openaiMessages, msgs...)
	}

	openaiReq := openai.ChatCompletionRequest{
		Model:    modelName,
		Messages: openaiMessages,
	}
	if req.Config.ThinkingConfig != nil {
		switch req.Config.ThinkingConfig.ThinkingLevel {
		case genai.ThinkingLevelLow:
			openaiReq.ReasoningEffort = "low"
		case genai.ThinkingLevelHigh:
			openaiReq.ReasoningEffort = "high"
		default:
			openaiReq.ReasoningEffort = "medium"
		}
	}
	if req.Config.ResponseSchema != nil {
		openaiSchema, err := genaiSchemaToOpenaiSchema(req.Config.ResponseSchema)
		if err != nil {
			return openai.ChatCompletionRequest{}, err
		}
		openaiReq.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type:       openai.ChatCompletionResponseFormatTypeJSONObject,
			JSONSchema: openaiSchema,
		}
	}

	if req.Config != nil && len(req.Config.Tools) > 0 {
		tools, err := convertTools(req.Config.Tools)
		if err != nil {
			return openai.ChatCompletionRequest{}, err
		}
		openaiReq.Tools = tools
	}

	if req.Config != nil {
		if req.Config.Temperature != nil {
			openaiReq.Temperature = *req.Config.Temperature
		}
		if req.Config.MaxOutputTokens > 0 {
			openaiReq.MaxTokens = int(req.Config.MaxOutputTokens)
		}
		if req.Config.TopP != nil {
			openaiReq.TopP = *req.Config.TopP
		}
		if len(req.Config.StopSequences) > 0 {
			openaiReq.Stop = req.Config.StopSequences
		}

		if req.Config.SystemInstruction != nil {
			systemMsg := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: extractTextFromContent(req.Config.SystemInstruction),
			}
			openaiMessages = append([]openai.ChatCompletionMessage{systemMsg}, openaiMessages...)
			openaiReq.Messages = openaiMessages
		}

		if req.Config.ResponseMIMEType == "application/json" {
			openaiReq.ResponseFormat = &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			}
		}
	}

	return openaiReq, nil
}

func toOpenAIChatCompletionMessage(content *genai.Content) ([]openai.ChatCompletionMessage, error) {
	toolRespMessages := make([]openai.ChatCompletionMessage, 0)
	skipIdx := 0
	for idx, part := range content.Parts {
		if part.FunctionResponse != nil {
			openaiMsg := openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: part.FunctionResponse.ID,
			}
			responseJSON, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal function response: %w", err)
			}
			openaiMsg.Content = string(responseJSON)
			toolRespMessages = append(toolRespMessages, openaiMsg)
			skipIdx = idx + 1
			continue
		}
	}

	parts := content.Parts[skipIdx:]
	if len(parts) == 0 {
		return toolRespMessages, nil
	}

	openaiMsg := openai.ChatCompletionMessage{
		Role: convertRoleToOpenAI(content.Role),
	}

	if len(parts) == 1 && parts[0].Text != "" {
		openaiMsg.Content = parts[0].Text
		return append(toolRespMessages, openaiMsg), nil
	}

	var textContent string
	var toolCalls []openai.ToolCall
	var multiContent []openai.ChatMessagePart

	for _, part := range parts {
		if part.Text != "" {
			if len(content.Parts) == 1 {
				textContent = part.Text
			} else {
				multiContent = append(multiContent, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: part.Text,
				})
			}
		}

		if part.FunctionCall != nil {
			argsJSON, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal function args: %w", err)
			}
			toolCall := openai.ToolCall{
				ID:   part.FunctionCall.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			}
			toolCalls = append(toolCalls, toolCall)
		}

		if part.FunctionResponse != nil {
			responseJSON, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal function response: %w", err)
			}
			openaiMsg.Role = openai.ChatMessageRoleTool
			openaiMsg.Content = string(responseJSON)
			openaiMsg.ToolCallID = part.FunctionResponse.ID
		}

		if part.InlineData != nil {
			switch part.InlineData.MIMEType {
			case "image/jpg", "image/jpeg", "image/png", "image/gif", "image/webp":
				base64Data := base64.StdEncoding.EncodeToString(part.InlineData.Data)
				imageURL := openai.ChatMessageImageURL{
					URL:    fmt.Sprintf("data:%s;base64,%s", part.InlineData.MIMEType, base64Data),
					Detail: openai.ImageURLDetailAuto,
				}
				multiContent = append(multiContent, openai.ChatMessagePart{
					Type:     openai.ChatMessagePartTypeImageURL,
					ImageURL: &imageURL,
				})
			default:
				multiContent = append(multiContent, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: string(part.InlineData.Data),
				})
			}
		}
	}

	if len(multiContent) > 0 {
		openaiMsg.MultiContent = multiContent
	} else if textContent != "" {
		openaiMsg.Content = textContent
	}

	if len(toolCalls) > 0 {
		openaiMsg.ToolCalls = toolCalls
	}

	return append(toolRespMessages, openaiMsg), nil
}

func convertChatCompletionResponse(resp *openai.ChatCompletionResponse) (*model.LLMResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, ErrNoChoicesInResponse
	}

	choice := resp.Choices[0]
	content := &genai.Content{
		Role:  genai.RoleModel,
		Parts: []*genai.Part{},
	}

	if choice.Message.Content != "" {
		content.Parts = append(content.Parts, &genai.Part{Text: choice.Message.Content})
	}

	for _, toolCall := range choice.Message.ToolCalls {
		if toolCall.Type == openai.ToolTypeFunction {
			content.Parts = append(content.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   toolCall.ID,
					Name: toolCall.Function.Name,
					Args: parseJSONArgs(toolCall.Function.Arguments),
				},
			})
		}
	}

	var usageMetadata *genai.GenerateContentResponseUsageMetadata
	if resp.Usage.TotalTokens > 0 {
		usageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(resp.Usage.PromptTokens),
			CandidatesTokenCount: int32(resp.Usage.CompletionTokens),
			TotalTokenCount:      int32(resp.Usage.TotalTokens),
		}
		if resp.Usage.PromptTokensDetails != nil {
			usageMetadata.CachedContentTokenCount = int32(resp.Usage.PromptTokensDetails.CachedTokens)
		}
	}

	return &model.LLMResponse{
		Content:       content,
		UsageMetadata: usageMetadata,
		FinishReason:  convertFinishReason(string(choice.FinishReason)),
		TurnComplete:  true,
	}, nil
}

func convertTools(genaiTools []*genai.Tool) ([]openai.Tool, error) {
	var openaiTools []openai.Tool

	for _, genaiTool := range genaiTools {
		if genaiTool == nil {
			continue
		}

		if genaiTool.GoogleSearch != nil ||
			genaiTool.CodeExecution != nil ||
			genaiTool.FileSearch != nil ||
			genaiTool.Retrieval != nil ||
			genaiTool.ComputerUse != nil {
			return nil, fmt.Errorf("only function tools are supported")
		}

		for _, funcDecl := range genaiTool.FunctionDeclarations {
			// Prefer ParametersJsonSchema (already a raw JSON Schema map).
			// Fall back to Parameters (*genai.Schema) converted via convertSchema().
			var params any
			if funcDecl.ParametersJsonSchema != nil {
				params = funcDecl.ParametersJsonSchema
			} else if funcDecl.Parameters != nil {
				converted, err := convertSchema(funcDecl.Parameters)
				if err != nil {
					return nil, fmt.Errorf("converting schema for tool %s: %w", funcDecl.Name, err)
				}
				params = converted
			}

			if params == nil {
				return nil, fmt.Errorf("no parameters schema for tool %s", funcDecl.Name)
			}

			openaiTools = append(openaiTools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        funcDecl.Name,
					Description: funcDecl.Description,
					Parameters:  params,
				},
			})
		}
	}

	return openaiTools, nil
}

func convertSchema(schema *genai.Schema) (map[string]any, error) {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}, nil
	}

	result := make(map[string]any)

	if schema.Type != genai.TypeUnspecified {
		result["type"] = convertSchemaType(schema.Type)
	}

	if schema.Description != "" {
		result["description"] = schema.Description
	}

	if len(schema.Properties) > 0 {
		properties := make(map[string]any)
		for propName, propSchema := range schema.Properties {
			convertedProp, err := convertSchema(propSchema)
			if err != nil {
				return nil, err
			}
			properties[propName] = convertedProp
		}
		result["properties"] = properties
	}

	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	if schema.Items != nil {
		items, err := convertSchema(schema.Items)
		if err != nil {
			return nil, err
		}
		result["items"] = items
	}

	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}

	return result, nil
}

// convertSchemaType maps genai.Type to JSON Schema type strings.
// Handles both uppercase constants (genai.TypeObject = "OBJECT") and
// lowercase values ("object") used in kasa tool declarations.
func convertSchemaType(t genai.Type) string {
	switch strings.ToUpper(string(t)) {
	case "STRING":
		return "string"
	case "NUMBER":
		return "number"
	case "INTEGER":
		return "integer"
	case "BOOLEAN":
		return "boolean"
	case "ARRAY":
		return "array"
	case "OBJECT":
		return "object"
	default:
		return "string"
	}
}

func convertRoleToOpenAI(role string) string {
	switch role {
	case "user":
		return openai.ChatMessageRoleUser
	case "model":
		return openai.ChatMessageRoleAssistant
	case "system":
		return openai.ChatMessageRoleSystem
	default:
		return openai.ChatMessageRoleUser
	}
}

func convertFinishReason(reason string) genai.FinishReason {
	switch reason {
	case "stop":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "tool_calls", "function_call":
		return genai.FinishReasonStop
	case "content_filter":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonUnspecified
	}
}

func extractTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var texts []string
	for _, part := range content.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func parseJSONArgs(argsJSON string) map[string]any {
	if argsJSON == "" {
		return make(map[string]any)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return make(map[string]any)
	}
	return args
}

func genaiSchemaToOpenaiSchema(schema *genai.Schema) (*openai.ChatCompletionResponseFormatJSONSchema, error) {
	tmp := map[string]any{
		"name":        "response",
		"description": schema.Description,
		"strict":      true,
		"schema":      schema,
	}
	jsonSchema, err := json.Marshal(tmp)
	if err != nil {
		return nil, err
	}

	var openaiSchema openai.ChatCompletionResponseFormatJSONSchema
	if err := openaiSchema.UnmarshalJSON(jsonSchema); err != nil {
		return nil, err
	}
	return &openaiSchema, nil
}

// openRouterTransport injects OpenRouter-specific HTTP headers.
type openRouterTransport struct {
	base http.RoundTripper
}

func (t *openRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("HTTP-Referer", "https://github.com/perbu/kasa")
	req.Header.Set("X-Title", "kasa")
	return t.base.RoundTrip(req)
}
