package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultModel = "mistral:7b-instruct-q4_K_M"

type ollamaNode struct {
	name    string
	baseURL string
}

// buildOllamaNodes constructs the Ollama endpoint list from config.
// Local node is always first; peer nodes are appended from cfg.PeerNodes.
// No IPs are hardcoded in source — all routing is driven by config/node.json.
func buildOllamaNodes(cfg NodeConfig) []ollamaNode {
	nodes := []ollamaNode{{name: "local", baseURL: "http://localhost:11434"}}
	for _, p := range cfg.PeerNodes {
		nodes = append(nodes, ollamaNode{name: p.Name, baseURL: p.BaseURL})
	}
	return nodes
}

type ollamaPsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// checkOllamaLoad calls /api/ps and returns the number of active models.
// A lower count means lighter load. Returns an error if the node is unreachable.
func checkOllamaLoad(baseURL string) (int, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/ps")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var ps ollamaPsResponse
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return 0, err
	}
	return len(ps.Models), nil
}

// selectBestNode returns the available Ollama node with the lowest load.
// Falls back to the first node if none respond (generate call will surface the error).
func selectBestNode(nodes []ollamaNode) ollamaNode {
	best := nodes[0]
	bestLoad := -1
	for _, n := range nodes {
		load, err := checkOllamaLoad(n.baseURL)
		if err != nil {
			continue
		}
		if bestLoad == -1 || load < bestLoad {
			bestLoad = load
			best = n
		}
	}
	return best
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// callOllamaAt sends a generate request to a specific Ollama endpoint.
func callOllamaAt(baseURL, model, prompt string) (string, error) {
	body, _ := json.Marshal(ollamaRequest{Model: model, Prompt: prompt, Stream: false})
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(baseURL+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama unreachable at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama at %s returned HTTP %d: %s", baseURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ollamaResp ollamaResponse
	if err := json.Unmarshal(raw, &ollamaResp); err != nil {
		return "", fmt.Errorf("parsing ollama response: %w", err)
	}
	return ollamaResp.Response, nil
}

// routedCallOllama selects the best available node and calls it.
// If the selected node fails, it tries remaining nodes before returning an error.
// Returns (response, routed_to_name, error).
func routedCallOllama(prompt, model string, nodes []ollamaNode) (string, string, error) {
	primary := selectBestNode(nodes)
	response, err := callOllamaAt(primary.baseURL, model, prompt)
	if err == nil {
		return response, primary.name, nil
	}
	fmt.Printf("  [!] ollama routing: %s failed (%v) — trying fallback nodes\n", primary.name, err)

	for _, n := range nodes {
		if n.baseURL == primary.baseURL {
			continue
		}
		response, ferr := callOllamaAt(n.baseURL, model, prompt)
		if ferr == nil {
			fmt.Printf("  [→] ollama routing: fell back to %s\n", n.name)
			return response, n.name, nil
		}
	}
	return "", "", fmt.Errorf("all ollama nodes unreachable (last error: %w)", err)
}

// ContextTurn is one message in a conversation history, as sent by the client.
type ContextTurn struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

// buildPromptWithContext prepends formatted conversation history to the current prompt
// so Ollama receives full context without any special token handling.
func buildPromptWithContext(ctx []ContextTurn, prompt string) string {
	if len(ctx) == 0 {
		return prompt
	}
	var sb strings.Builder
	sb.WriteString("[INST] ")
	sb.WriteString("Here is our conversation so far:\n")
	for _, turn := range ctx {
		role := "User"
		if turn.Role == "assistant" {
			role = "Assistant"
		}
		sb.WriteString(role + ": " + turn.Content + "\n")
	}
	sb.WriteString("\nCurrent message: ")
	sb.WriteString(prompt)
	sb.WriteString(" [/INST]")
	return sb.String()
}

// braveSearchResponse holds the fields we use from the Brave Search API.
type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// searchWeb queries the Brave Search API and returns a compact formatted string
// suitable for prepending to a prompt. Falls back gracefully when apiKey is empty.
func searchWeb(query, apiKey string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("Brave API key not configured")
	}
	endpoint := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=5"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("building Brave request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Brave request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Brave API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var brave braveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&brave); err != nil {
		return "", fmt.Errorf("Brave parse error: %w", err)
	}
	if len(brave.Web.Results) == 0 {
		return "", fmt.Errorf("Brave returned no results for %q", query)
	}

	var sb strings.Builder
	sb.WriteString("[Web results for: " + query + "]\n")
	for i, r := range brave.Web.Results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   %s\n", i+1, r.Title, r.URL, r.Description))
	}
	return sb.String(), nil
}

// needsWebSearch checks the prompt for keywords that signal a need for live/current data.
func needsWebSearch(prompt string) (bool, string) {
	lower := strings.ToLower(prompt)
	keywords := []string{
		"today", "current", "latest", "news", "weather", "price",
		"score", " now", "recently", "who is", "what is happening", "stock",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true, prompt
		}
	}
	return false, ""
}
