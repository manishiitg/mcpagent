package virtual_tools

import (
	"encoding/json"
	"fmt"
)

type SearchLargeOutputParams struct {
	// Name of the tool output file (e.g., tool_20250721_091511_tavily-search.json)
	Filename string `json:"filename"`
	// Operation type: "read" for character range reading, "search" for regex pattern matching, "query" for jq JSON queries
	Operation string `json:"operation"`
	// Starting character position (1-based). Required when operation="read"
	Start *int `json:"start,omitempty"`
	// Ending character position (inclusive). Required when operation="read"
	End *int `json:"end,omitempty"`
	// Search pattern (regex supported). Required when operation="search"
	Pattern *string `json:"pattern,omitempty"`
	// Case sensitive search. Used when operation="search"
	Case_sensitive *bool `json:"case_sensitive,omitempty"`
	// Maximum number of results to return. Used when operation="search"
	Max_results *int `json:"max_results,omitempty"`
	// jq query to execute (e.g., '.name', '.items[]'). Required when operation="query"
	Query *string `json:"query,omitempty"`
	// Output compact JSON format. Used when operation="query"
	Compact *bool `json:"compact,omitempty"`
	// Output raw string values. Used when operation="query"
	Raw *bool `json:"raw,omitempty"`
}

// Access offloaded tool output files through read, search, or query operations
//
// Usage: Import package and call with typed struct
//
//	Panics on API errors - check output string for tool execution errors
//
//	Example (read operation): output := SearchLargeOutput(SearchLargeOutputParams{
//	    Filename: "tool_20250721_091511_tavily-search.json",
//	    Operation: "read",
//	    Start: intPtr(1),
//	    End: intPtr(5000),
//	})
//
//	Example (search operation): output := SearchLargeOutput(SearchLargeOutputParams{
//	    Filename: "tool_20250721_091511_logs.txt",
//	    Operation: "search",
//	    Pattern: stringPtr("ERROR|FATAL"),
//	    Case_sensitive: boolPtr(false),
//	    Max_results: intPtr(50),
//	})
//
//	Example (query operation): output := SearchLargeOutput(SearchLargeOutputParams{
//	    Filename: "tool_20250721_091511_api-response.json",
//	    Operation: "query",
//	    Query: stringPtr(".results[] | select(.status == \"active\") | .name"),
//	    Compact: boolPtr(true),
//	})
//
// // Check output for errors (e.g., strings.HasPrefix(output, "Error:"))
// // Handle tool execution error if detected
func SearchLargeOutput(params SearchLargeOutputParams) string {
	// Convert params struct to map for API call
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal parameters: %%v", err))
	}
	var paramsMap map[string]interface{}
	if err := json.Unmarshal(paramsBytes, &paramsMap); err != nil {
		panic(fmt.Sprintf("failed to unmarshal parameters: %%v", err))
	}

	// Build request payload and call common API client
	payload := map[string]interface{}{
		"tool": "search_large_output",
		"args": paramsMap,
	}
	return callAPI("/api/virtual/execute", payload)
}
