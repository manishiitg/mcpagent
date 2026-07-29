package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

type youSearchResponse struct {
	Results struct {
		Web []struct {
			Title       string   `json:"title"`
			URL         string   `json:"url"`
			Description string   `json:"description"`
			Snippets    []string `json:"snippets"`
		} `json:"web"`
	} `json:"results"`
}

func main() {
	_ = godotenv.Load()

	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY is required")
		os.Exit(1)
	}

	llmModel, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderOpenAI,
		ModelID:  "gpt-4o-mini",
		APIKeys:  &llm.ProviderAPIKeys{OpenAI: &openAIKey},
	})
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	agent, err := mcpagent.NewAgent(ctx, llmModel, "mcp_servers.json")
	if err != nil {
		panic(err)
	}

	youSearchParams := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of web results to return (1-20)",
			},
		},
		"required": []string{"query"},
	}

	err = agent.RegisterCustomTool(
		"you_search",
		"Search the web using the you.com Search API. Uses YDC_API_KEY when present, otherwise uses free-tier unauthenticated requests (100/day).",
		youSearchParams,
		youSearchTool,
		"data",
	)
	if err != nil {
		panic(err)
	}

	prompt := "Use you_search to find the latest MCPAgent Go library release notes and summarize in 3 bullets with links."
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	resp, err := agent.Ask(ctx, prompt)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp)
}

func youSearchTool(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	count := 5
	if raw, ok := args["count"]; ok {
		switch v := raw.(type) {
		case float64:
			count = int(v)
		case int:
			count = v
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				count = n
			}
		}
	}
	if count < 1 {
		count = 1
	}
	if count > 20 {
		count = 20
	}

	endpoint := "https://api.you.com/v1/agents/search"
	vals := url.Values{}
	vals.Set("query", query)
	vals.Set("count", strconv.Itoa(count))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+vals.Encode(), nil)
	if err != nil {
		return "", err
	}

	if key := strings.TrimSpace(os.Getenv("YDC_API_KEY")); key != "" {
		req.Header.Set("X-API-Key", key)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("you.com search request failed: %w", err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if (res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 429) && strings.TrimSpace(os.Getenv("YDC_API_KEY")) == "" {
			return "you_search unavailable on unauthenticated free tier right now. Set YDC_API_KEY and retry.", nil
		}
		return "", fmt.Errorf("you.com search failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	var data youSearchResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("could not parse you.com response: %w", err)
	}

	if len(data.Results.Web) == 0 {
		return "No web results returned from you.com for this query.", nil
	}

	var b strings.Builder
	for i, r := range data.Results.Web {
		snippet := r.Description
		if len(r.Snippets) > 0 && strings.TrimSpace(r.Snippets[0]) != "" {
			snippet = r.Snippets[0]
		}
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.URL), strings.TrimSpace(snippet))
	}

	return b.String(), nil
}
