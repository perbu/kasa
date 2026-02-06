package tools

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

const (
	httpRequestTimeout     = 15 * time.Second
	httpResponseBodyMaxLen = 256
)

// HTTPRequestTool provides HTTP request capability for verifying deployments.
type HTTPRequestTool struct{}

// NewHTTPRequestTool creates a new HTTPRequestTool.
func NewHTTPRequestTool() *HTTPRequestTool {
	return &HTTPRequestTool{}
}

// Name returns the tool name.
func (t *HTTPRequestTool) Name() string {
	return "http_request"
}

// Description returns the tool description.
func (t *HTTPRequestTool) Description() string {
	return "Make an HTTP GET or HEAD request to verify a deployed service is accessible. Useful for checking if a deployment with an HTTPRoute/Ingress is responding correctly. Only GET and HEAD methods are allowed for security."
}

// IsLongRunning returns false.
func (t *HTTPRequestTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *HTTPRequestTool) Category() ToolCategory {
	return CategoryMutating // Requires approval since it makes external requests
}

// ProcessRequest adds this tool to the LLM request.
func (t *HTTPRequestTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *HTTPRequestTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"url": {
					Type:        "string",
					Description: "The URL to request (must start with http:// or https://)",
				},
				"method": {
					Type:        "string",
					Description: "HTTP method: GET or HEAD (default: GET)",
					Enum:        []string{"GET", "HEAD"},
				},
				"headers": {
					Type:        "object",
					Description: "Optional HTTP headers to send (e.g., {\"Host\": \"myapp.example.com\"} when DNS is not yet updated)",
				},
			},
			Required: []string{"url"},
		},
	}
}

// Run executes the tool.
func (t *HTTPRequestTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return map[string]any{"error": "invalid arguments"}, nil
	}

	// Parse URL
	urlRaw, ok := argsMap["url"]
	if !ok {
		return map[string]any{"error": "url parameter is required"}, nil
	}
	url, ok := urlRaw.(string)
	if !ok {
		return map[string]any{"error": "url must be a string"}, nil
	}

	// Validate URL scheme
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return map[string]any{"error": "url must start with http:// or https://"}, nil
	}

	// Parse method (default GET)
	method := "GET"
	if methodRaw, ok := argsMap["method"]; ok {
		if m, ok := methodRaw.(string); ok {
			method = strings.ToUpper(m)
		}
	}

	// Validate method
	if method != "GET" && method != "HEAD" {
		return map[string]any{"error": "method must be GET or HEAD"}, nil
	}

	// Parse headers
	headers := make(map[string]string)
	if headersRaw, ok := argsMap["headers"]; ok {
		if h, ok := headersRaw.(map[string]any); ok {
			for k, v := range h {
				if vs, ok := v.(string); ok {
					headers[k] = vs
				}
			}
		}
	}

	// Create request
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to create request: %v", err)}, nil
	}

	// Set headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Set a reasonable User-Agent
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "kasa/1.0 (Kubernetes deployment verification)")
	}

	// Create client with timeout and redirect following
	client := &http.Client{
		Timeout: httpRequestTimeout,
		// Default behavior follows redirects (up to 10)
	}

	// Execute request
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		return map[string]any{
			"error":      fmt.Sprintf("request failed: %v", err),
			"elapsed_ms": elapsed.Milliseconds(),
		}, nil
	}
	defer resp.Body.Close()

	// Build result
	result := map[string]any{
		"status_code": resp.StatusCode,
		"status":      resp.Status,
		"elapsed_ms":  elapsed.Milliseconds(),
	}

	// Include selected response headers
	responseHeaders := make(map[string]string)
	for _, name := range []string{"Content-Type", "Content-Length", "Server", "Location", "X-Powered-By"} {
		if v := resp.Header.Get(name); v != "" {
			responseHeaders[name] = v
		}
	}
	if len(responseHeaders) > 0 {
		result["headers"] = responseHeaders
	}

	// Read body (only for GET, and truncate)
	if method == "GET" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, httpResponseBodyMaxLen+1))
		if err != nil {
			result["body_error"] = fmt.Sprintf("failed to read body: %v", err)
		} else {
			bodyStr := string(body)
			if len(body) > httpResponseBodyMaxLen {
				bodyStr = string(body[:httpResponseBodyMaxLen])
				result["body_truncated"] = true
			}
			result["body"] = bodyStr
		}
	}

	return result, nil
}
