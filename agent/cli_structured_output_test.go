package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// --- isCLIProvider tests ---

func TestIsCLIProvider(t *testing.T) {
	tests := []struct {
		provider llm.Provider
		want     bool
	}{
		{llm.ProviderClaudeCode, true},
		{llm.ProviderGeminiCLI, true},
		{llm.ProviderAnthropic, false},
		{llm.ProviderOpenAI, false},
		{llm.Provider("unknown"), false},
	}
	for _, tt := range tests {
		if got := isCLIProvider(tt.provider); got != tt.want {
			t.Errorf("isCLIProvider(%q) = %v, want %v", tt.provider, got, tt.want)
		}
	}
}

// --- extractJSONFromCLIResponse tests ---

func TestExtractJSONFromCLIResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "direct valid JSON object",
			input: `{"key": "value", "num": 42}`,
			want:  `{"key": "value", "num": 42}`,
		},
		{
			name:  "direct valid JSON with whitespace",
			input: `  {"key": "value"}  `,
			want:  `{"key": "value"}`,
		},
		{
			name:  "markdown json code block",
			input: "```json\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "markdown plain code block",
			input: "```\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "text before and after JSON object",
			input: "Here is the result:\n{\"status\": \"ok\"}\nDone!",
			want:  `{"status": "ok"}`,
		},
		{
			name:  "valid JSON array",
			input: `[1, 2, 3]`,
			want:  `[1, 2, 3]`,
		},
		{
			name:  "text around JSON array",
			input: "Result: [1, 2, 3] end",
			want:  `[1, 2, 3]`,
		},
		{
			name:  "nested JSON object",
			input: `Some text {"outer": {"inner": true}} more text`,
			want:  `{"outer": {"inner": true}}`,
		},
		{
			name:    "empty response",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   \n\t  ",
			wantErr: true,
		},
		{
			name:    "no JSON at all",
			input:   "This is just plain text with no JSON.",
			wantErr: true,
		},
		{
			name:    "invalid JSON object",
			input:   `{"key": "value"`,
			wantErr: true,
		},
		{
			name:  "markdown code block with extra whitespace",
			input: "```json\n  {\"a\": 1}  \n```",
			want:  `{"a": 1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONFromCLIResponse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("extractJSONFromCLIResponse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && string(got) != tt.want {
				t.Errorf("extractJSONFromCLIResponse() = %q, want %q", string(got), tt.want)
			}
		})
	}
}

// --- injectStructuredOutputIntoLastUserMessage tests ---

func TestInjectStructuredOutputIntoLastUserMessage(t *testing.T) {
	instruction := "\nRESPOND WITH JSON"

	t.Run("appends to last human message", func(t *testing.T) {
		messages := []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hello"}}},
			{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Question"}}},
		}

		result := injectStructuredOutputIntoLastUserMessage(messages, instruction)

		// Result should have same length
		if len(result) != len(messages) {
			t.Fatalf("expected %d messages, got %d", len(messages), len(result))
		}

		// Last human message (index 2) should have 2 parts now
		lastHuman := result[2]
		if len(lastHuman.Parts) != 2 {
			t.Fatalf("expected 2 parts in last human message, got %d", len(lastHuman.Parts))
		}

		// Second part should be the instruction
		tc, ok := lastHuman.Parts[1].(llmtypes.TextContent)
		if !ok {
			t.Fatal("expected TextContent as second part")
		}
		if tc.Text != instruction {
			t.Errorf("instruction text = %q, want %q", tc.Text, instruction)
		}
	})

	t.Run("does not mutate original messages", func(t *testing.T) {
		originalParts := []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hello"}}
		messages := []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: originalParts},
		}

		_ = injectStructuredOutputIntoLastUserMessage(messages, instruction)

		// Original should still have 1 part
		if len(messages[0].Parts) != 1 {
			t.Errorf("original message was mutated: expected 1 part, got %d", len(messages[0].Parts))
		}
	})

	t.Run("no human message creates one", func(t *testing.T) {
		messages := []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}}},
		}

		result := injectStructuredOutputIntoLastUserMessage(messages, instruction)

		if len(result) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(result))
		}

		if result[1].Role != llmtypes.ChatMessageTypeHuman {
			t.Errorf("expected new message role = human, got %s", result[1].Role)
		}

		tc, ok := result[1].Parts[0].(llmtypes.TextContent)
		if !ok {
			t.Fatal("expected TextContent")
		}
		if tc.Text != instruction {
			t.Errorf("instruction text = %q, want %q", tc.Text, instruction)
		}
	})

	t.Run("targets last human message not first", func(t *testing.T) {
		messages := []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "First"}}},
			{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Response"}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Second"}}},
		}

		result := injectStructuredOutputIntoLastUserMessage(messages, instruction)

		// First human message should be untouched
		if len(result[0].Parts) != 1 {
			t.Errorf("first human message was modified: expected 1 part, got %d", len(result[0].Parts))
		}

		// Last human message should have the instruction
		if len(result[2].Parts) != 2 {
			t.Errorf("last human message expected 2 parts, got %d", len(result[2].Parts))
		}
	})
}

// --- buildCLIStructuredOutputInstruction tests ---

func TestBuildCLIStructuredOutputInstruction(t *testing.T) {
	schema := `{"type":"object","properties":{"name":{"type":"string"}}}`

	result := buildCLIStructuredOutputInstruction(schema)

	if !containsSubstring(result, "ONLY a valid JSON object") {
		t.Error("instruction should mention 'ONLY a valid JSON object'")
	}
	if !containsSubstring(result, schema) {
		t.Error("instruction should contain the schema")
	}
	if !containsSubstring(result, "JSON Schema:") {
		t.Error("instruction should contain 'JSON Schema:' label")
	}
}

func TestBuildCLIStructuredOutputInstructionWithTool(t *testing.T) {
	schema := `{"type":"object","properties":{"status":{"type":"string"}}}`
	toolName := "submit_report"
	toolDesc := "Submit the final report"

	result := buildCLIStructuredOutputInstructionWithTool(toolName, toolDesc, schema)

	if !containsSubstring(result, toolName) {
		t.Errorf("instruction should contain tool name %q", toolName)
	}
	if !containsSubstring(result, toolDesc) {
		t.Errorf("instruction should contain tool description %q", toolDesc)
	}
	if !containsSubstring(result, schema) {
		t.Error("instruction should contain the schema")
	}
}

// --- stripMarkdownCodeBlock tests ---

func TestStripMarkdownCodeBlock(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"json block", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"plain block", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"not a code block", `{"a":1}`, ""},
		{"partial block", "```json\n{\"a\":1}", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripMarkdownCodeBlock(tt.input); got != tt.want {
				t.Errorf("stripMarkdownCodeBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- extractOutermost tests ---

func TestExtractOutermost(t *testing.T) {
	tests := []struct {
		name  string
		input string
		open  byte
		close byte
		want  string
	}{
		{"simple object", `text {"a":1} end`, '{', '}', `{"a":1}`},
		{"nested", `text {"a":{"b":2}} end`, '{', '}', `{"a":{"b":2}}`},
		{"array", `text [1,2] end`, '[', ']', `[1,2]`},
		{"no open", `text end`, '{', '}', ""},
		{"no close", `text { end`, '{', '}', ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractOutermost(tt.input, tt.open, tt.close); got != tt.want {
				t.Errorf("extractOutermost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && contains(s, substr)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
