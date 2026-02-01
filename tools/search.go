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

// SearchWebTool provides the search_web tool for web search via Tavily API.
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

// tavilyRequest represents the request body for Tavily API.
type tavilyRequest struct {
	APIKey      string `json:"api_key"`
	Query       string `json:"query"`
	SearchDepth string `json:"search_depth"`
	MaxResults  int    `json:"max_results"`
}

// tavilyResponse represents the response from Tavily API.
type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

// tavilyResult represents a single search result from Tavily.
type tavilyResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Run executes the tool.
func (t *SearchWebTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return map[string]any{"error": "invalid arguments"}, nil
	}

	query, ok := argsMap["query"].(string)
	if !ok || query == "" {
		return map[string]any{"error": "query parameter is required"}, nil
	}

	// Check if API key is configured
	if t.apiKey == "" {
		return map[string]any{"error": "TAVILY_API_KEY not configured"}, nil
	}

	// Create request body
	reqBody := tavilyRequest{
		APIKey:      t.apiKey,
		Query:       query,
		SearchDepth: "basic",
		MaxResults:  5,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal request: %v", err)}, nil
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.tavily.com/search", bytes.NewBuffer(jsonBody))
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to create request: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to execute search: %v", err)}, nil
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to read response: %v", err)}, nil
	}

	// Check for non-200 status
	if resp.StatusCode != http.StatusOK {
		return map[string]any{"error": fmt.Sprintf("search API returned status %d: %s", resp.StatusCode, string(body))}, nil
	}

	// Parse response
	var tavilyResp tavilyResponse
	if err := json.Unmarshal(body, &tavilyResp); err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to parse response: %v", err)}, nil
	}

	// Convert results to generic format
	results := make([]map[string]any, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Content,
		})
	}

	return map[string]any{
		"query":   query,
		"results": results,
	}, nil
}
