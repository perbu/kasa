package tools

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// FetchUrlTool provides the fetch_url tool for fetching web content via Jina Reader API.
type FetchUrlTool struct {
	apiKey string
}

// NewFetchUrlTool creates a new FetchUrlTool.
func NewFetchUrlTool(apiKey string) *FetchUrlTool {
	return &FetchUrlTool{
		apiKey: apiKey,
	}
}

// Name returns the tool name.
func (t *FetchUrlTool) Name() string {
	return "fetch_url"
}

// Description returns the tool description.
func (t *FetchUrlTool) Description() string {
	return "Fetch content from a URL and return it as markdown. Useful for reading documentation, Docker Hub pages, or any web content."
}

// IsLongRunning returns false as this is typically a quick operation.
func (t *FetchUrlTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *FetchUrlTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *FetchUrlTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *FetchUrlTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"url": {
					Type:        "string",
					Description: "The URL to fetch content from",
				},
			},
			Required: []string{"url"},
		},
	}
}

// Run executes the tool.
func (t *FetchUrlTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return map[string]any{"error": "invalid arguments"}, nil
	}

	url, ok := argsMap["url"].(string)
	if !ok || url == "" {
		return map[string]any{"error": "url parameter is required"}, nil
	}

	// Check if API key is configured
	if t.apiKey == "" {
		return map[string]any{"error": "JINA_READER_API_KEY not configured"}, nil
	}

	// Create request to Jina Reader API
	jinaURL := "https://r.jina.ai/" + url
	req, err := http.NewRequest("GET", jinaURL, nil)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to create request: %v", err)}, nil
	}

	// Add authorization header
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	// Execute request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to fetch URL: %v", err)}, nil
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to read response: %v", err)}, nil
	}

	// Truncate if too long (Gemini has context limits)
	content := string(body)
	const maxContentLength = 50000
	if len(content) > maxContentLength {
		content = content[:maxContentLength] + "\n\n[Content truncated due to length...]"
	}

	return map[string]any{
		"url":         url,
		"content":     content,
		"status_code": resp.StatusCode,
	}, nil
}
