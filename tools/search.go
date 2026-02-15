package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// SearchWebTool provides the search_web tool for web search via Jina Search API.
type SearchWebTool struct {
	apiKey string
}

// NewSearchWebTool creates a new SearchWebTool.
func NewSearchWebTool(apiKey string) *SearchWebTool {
	return &SearchWebTool{
		apiKey: apiKey,
	}
}

// Name returns the tool name.
func (t *SearchWebTool) Name() string {
	return "search_web"
}

// Description returns the tool description.
func (t *SearchWebTool) Description() string {
	return "Search the web for information. Returns a list of relevant results with titles, URLs, and snippets."
}

// IsLongRunning returns false as this is typically a quick operation.
func (t *SearchWebTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *SearchWebTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *SearchWebTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *SearchWebTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"query": {
					Type:        "string",
					Description: "The search query",
				},
			},
			Required: []string{"query"},
		},
	}
}

// jinaSearchRequest represents the request body for Jina Search API.
type jinaSearchRequest struct {
	Query string `json:"q"`
}

// jinaSearchResponse represents the response from Jina Search API.
type jinaSearchResponse struct {
	Code   int                `json:"code"`
	Status int                `json:"status"`
	Data   []jinaSearchResult `json:"data"`
}

// jinaSearchResult represents a single search result from Jina.
type jinaSearchResult struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Content     string `json:"content"`
}

// Run executes the tool.
func (t *SearchWebTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	query, ok := argsMap["query"].(string)
	if !ok || query == "" {
		return errorResult("query parameter is required")
	}

	// Check if API key is configured
	if t.apiKey == "" {
		return errorResult("JINA_API_KEY not configured")
	}

	// Create request body
	reqBody := jinaSearchRequest{
		Query: query,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to marshal request: %v", err))
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://s.jina.ai/", bytes.NewBuffer(jsonBody))
	if err != nil {
		return errorResult(fmt.Sprintf("failed to create request: %v", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	// Execute request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to execute search: %v", err))
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read response: %v", err))
	}

	// Check for non-200 status
	if resp.StatusCode != http.StatusOK {
		return errorResult(fmt.Sprintf("search API returned status %d: %s", resp.StatusCode, string(body)))
	}

	// Parse response
	var jinaResp jinaSearchResponse
	if err := json.Unmarshal(body, &jinaResp); err != nil {
		return errorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	// Convert results to generic format
	results := make([]map[string]any, 0, len(jinaResp.Data))
	for _, r := range jinaResp.Data {
		snippet := r.Description
		if snippet == "" {
			snippet = r.Content
		}
		// Truncate long content snippets
		const maxSnippetLen = 500
		if len(snippet) > maxSnippetLen {
			snippet = snippet[:maxSnippetLen] + "..."
		}
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": snippet,
		})
	}

	return map[string]any{
		"query":   query,
		"results": results,
	}, nil
}
