package mcpagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/utils"
)

const (
	// DefaultLargeToolOutputThreshold is the default token threshold for context offloading
	// When tool outputs exceed this threshold (in tokens), they are offloaded to filesystem (offload context pattern)
	// Note: The threshold is compared against token count, not character count
	DefaultLargeToolOutputThreshold = 10000

	// DefaultToolOutputFolder is the default folder for storing offloaded tool outputs
	DefaultToolOutputFolder = "tool_output_folder"
)

// ToolOutputHandler implements context offloading by writing large tool outputs to files
// This follows the "offload context" pattern where tool results are stored externally
// and accessed on-demand to prevent context window overflow
type ToolOutputHandler struct {
	Threshold       int
	OutputFolder    string
	SessionID       string // Session ID for organizing files by conversation
	Enabled         bool
	ServerAvailable bool                // Whether context offloading virtual tools are available
	LLM             llmtypes.Model      // Optional LLM model for provider-aware token counting
	tokenCounter    *utils.TokenCounter // Cached token counter instance
}

// NewToolOutputHandler creates a new tool output handler with default settings
func NewToolOutputHandler() *ToolOutputHandler {
	return &ToolOutputHandler{
		Threshold:       DefaultLargeToolOutputThreshold,
		OutputFolder:    DefaultToolOutputFolder,
		SessionID:       "",
		Enabled:         true,
		ServerAvailable: false, // Will be set by agent
		tokenCounter:    utils.NewTokenCounter(),
	}
}

// NewToolOutputHandlerWithConfig creates a new tool output handler with custom settings
func NewToolOutputHandlerWithConfig(threshold int, outputFolder string, sessionID string, enabled bool, serverAvailable bool) *ToolOutputHandler {
	return &ToolOutputHandler{
		Threshold:       threshold,
		OutputFolder:    outputFolder,
		SessionID:       sessionID,
		Enabled:         enabled,
		ServerAvailable: serverAvailable,
		tokenCounter:    utils.NewTokenCounter(),
	}
}

// IsLargeToolOutput checks if the tool output exceeds the threshold for context offloading
// Returns true if output should be offloaded to filesystem (offload context pattern)
func (h *ToolOutputHandler) IsLargeToolOutput(content string) bool {
	contentLength := len(content)

	if !h.Enabled {
		return false
	}
	// Context offloading uses virtual tools that handle file operations directly
	// No MCP server dependency needed for offloading

	return contentLength > h.Threshold
}

// IsLargeToolOutputWithModel checks if the tool output exceeds the threshold using token counting
// Used for context offloading to determine when to save outputs to filesystem
func (h *ToolOutputHandler) IsLargeToolOutputWithModel(content string, model string) bool {
	if !h.Enabled {
		return false
	}

	// Use token counting instead of character counting
	tokenCount := h.CountTokensForModel(content, model)
	return tokenCount > h.Threshold
}

// SetLLM sets the LLM model for provider-aware token counting
func (h *ToolOutputHandler) SetLLM(llm llmtypes.Model) {
	h.LLM = llm
}

// CountTokensForModel counts tokens for the given content using provider/model-specific encoding
// It uses the LLM model's metadata to determine the correct encoding, or falls back to provider-based encoding
func (h *ToolOutputHandler) CountTokensForModel(content string, modelID string) int {
	// Initialize token counter if not already initialized
	if h.tokenCounter == nil {
		h.tokenCounter = utils.NewTokenCounter()
	}

	// Try to use LLM model for accurate token counting
	if h.LLM != nil {
		// Get model metadata from LLM
		metadata, err := h.LLM.GetModelMetadata(modelID)
		if err == nil && metadata != nil {
			// Use provider-aware token counting
			tokenCount, err := h.tokenCounter.CountTokens(content, metadata)
			if err == nil {
				return tokenCount
			}
		}

		// Fallback: try to get provider from model ID or use CountTokensForModel with LLM
		// Extract provider from model metadata if available
		if metadata != nil && metadata.Provider != "" {
			tokenCount, err := h.tokenCounter.CountTokensForProvider(content, metadata.Provider, modelID)
			if err == nil {
				return tokenCount
			}
		}
	}

	// Fallback: use provider-based encoding detection from model ID
	// Try to infer provider from model ID
	provider := inferProviderFromModelID(modelID)
	tokenCount, err := h.tokenCounter.CountTokensForProvider(content, provider, modelID)
	if err == nil {
		return tokenCount
	}

	// If tiktoken fails completely, return 0 (character-based estimation removed)
	// This means large output detection may not work if tiktoken fails
	return 0
}

// inferProviderFromModelID attempts to infer the provider from the model ID
func inferProviderFromModelID(modelID string) string {
	modelIDLower := strings.ToLower(modelID)

	// Check for OpenAI/OpenRouter models
	if strings.HasPrefix(modelIDLower, "gpt-") || strings.HasPrefix(modelIDLower, "o1") || strings.HasPrefix(modelIDLower, "o3") {
		return "openai"
	}

	// Check for Anthropic models
	if strings.Contains(modelIDLower, "claude") {
		return "anthropic"
	}

	// Check for Google/Gemini models
	if strings.Contains(modelIDLower, "gemini") {
		return "google"
	}

	// Check for Bedrock models (usually have specific prefixes)
	if strings.Contains(modelIDLower, "anthropic.claude") || strings.Contains(modelIDLower, "amazon.") {
		return "bedrock"
	}

	// Default fallback
	return ""
}

// WriteToolOutputToFile offloads large tool output to filesystem (context offloading)
// Returns the file path where the content was saved
func (h *ToolOutputHandler) WriteToolOutputToFile(content, toolName string) (string, error) {
	if !h.Enabled {
		return "", fmt.Errorf("tool output handler is disabled")
	}

	// Extract actual content from prefixed tool result
	actualContent := ExtractActualContent(content)

	// Create session-based folder path
	var sessionFolder string
	if h.SessionID != "" {
		sessionFolder = filepath.Join(h.OutputFolder, h.SessionID)
	} else {
		sessionFolder = h.OutputFolder
	}

	// Ensure output directory exists
	if err := os.MkdirAll(sessionFolder, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for user-accessible directories
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Generate unique filename with appropriate extension
	filename := h.generateToolOutputFilename(toolName, actualContent)
	filePath := filepath.Join(sessionFolder, filename)

	// Write actual content to file (without prefix)
	if err := os.WriteFile(filePath, []byte(actualContent), 0644); err != nil { //nolint:gosec // 0644 permissions are intentional for user-accessible files
		return "", fmt.Errorf("failed to write tool output to file: %w", err)
	}

	return filePath, nil
}

// generateToolOutputFilename creates a unique filename for tool output
func (h *ToolOutputHandler) generateToolOutputFilename(toolName string, content string) string {
	timestamp := time.Now().Format("20060102_150405")
	// Sanitize tool name for filename
	sanitizedName := sanitizeFilename(toolName)
	return fmt.Sprintf("tool_%s_%s%s", timestamp, sanitizedName, h.getFileExtension(content))
}

// sanitizeFilename removes or replaces characters that are not safe for filenames
func sanitizeFilename(name string) string {
	// Replace unsafe characters with underscores
	unsafeChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	sanitized := name
	for _, char := range unsafeChars {
		sanitized = replaceAll(sanitized, char, "_")
	}

	// Limit length to avoid filesystem issues
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}

	return sanitized
}

// replaceAll is a simple string replacement function
func replaceAll(s, old, new string) string {
	result := ""
	start := 0
	for i := 0; i < len(s); i++ {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += s[start:i] + new
			start = i + len(old)
			i += len(old) - 1
		}
	}
	result += s[start:]
	return result
}

// CreateToolOutputMessageWithPreview creates a message for the LLM with file path, first characters up to threshold, and instructions
// previewPercent: percentage of threshold to use for preview (e.g., 50 for 50%, 10 for 10%)
// isContextEditing: if true, creates a concise message for stale responses (context editing); if false, creates detailed message for new offloading
func (h *ToolOutputHandler) CreateToolOutputMessageWithPreview(toolCallID, filePath, content string, previewPercent int, isContextEditing bool) string {
	// Extract actual content from prefixed tool result
	actualContent := ExtractActualContent(content)

	// Extract first characters based on preview percentage
	previewLength := (h.Threshold * previewPercent) / 100
	preview := h.ExtractFirstNCharacters(actualContent, previewLength)

	// Use the full relative path so LLM knows which session folder to use
	// This fixes session ID mismatch issues when agent instances change
	fullRelativePath := filePath
	// Normalize path separators for cross-platform compatibility
	fullRelativePath = strings.ReplaceAll(fullRelativePath, "\\", "/")

	var instructions string
	if isContextEditing {
		// Concise message for context editing (stale responses) - LLM already knows how to use tools
		instructions = fmt.Sprintf(`Tool output saved to: %s

Preview (%d chars): %s

[Use search_large_output tool to access full content]`, fullRelativePath, previewLength, preview)
	} else {
		// Detailed message for context offloading (new large outputs)
		instructions = fmt.Sprintf(`
The tool output was too large and has been saved to: %s

FIRST %d CHARACTERS OF OUTPUT (%d%% of threshold):
%s

[Content truncated for display - full content available in file]

Make sure to use the virtual tool next to read contents of this file in an efficient manner:

Available virtual tool for context offloading:
- search_large_output - unified tool for accessing offloaded files. Use operation='read' to read character ranges, operation='search' for regex pattern matching, or operation='query' for jq JSON queries.

Example: Use search_large_output with operation='read' (start/end params), operation='search' (pattern param), or operation='query' (query param for jq)

NOTE: When using the virtual tool, you can provide either:
- The full path: "%s" (recommended - includes session folder)
- Or just the filename: "%s" (will use current session folder)
`, fullRelativePath, previewLength, previewPercent, preview, fullRelativePath, filepath.Base(filePath))
	}

	return instructions
}

// ExtractFirstNCharacters extracts the first n characters from content
func (h *ToolOutputHandler) ExtractFirstNCharacters(content string, n int) string {
	if len(content) <= n {
		return content
	}
	return content[:n]
}

// GetToolOutputFolder returns the current output folder path
func (h *ToolOutputHandler) GetToolOutputFolder() string {
	return h.OutputFolder
}

// SetThreshold updates the threshold for context offloading (when to offload tool outputs)
func (h *ToolOutputHandler) SetThreshold(threshold int) {
	h.Threshold = threshold
}

// SetOutputFolder updates the output folder path
func (h *ToolOutputHandler) SetOutputFolder(folder string) {
	h.OutputFolder = folder
}

// SetEnabled enables or disables the tool output handler
func (h *ToolOutputHandler) SetEnabled(enabled bool) {
	h.Enabled = enabled
}

// SetServerAvailable sets whether context offloading virtual tools are available
func (h *ToolOutputHandler) SetServerAvailable(available bool) {
	h.ServerAvailable = available
}

// IsServerAvailable returns whether context offloading virtual tools are available
func (h *ToolOutputHandler) IsServerAvailable() bool {
	return h.ServerAvailable
}

// SetSessionID sets the session ID for organizing files by conversation
func (h *ToolOutputHandler) SetSessionID(sessionID string) {
	h.SessionID = sessionID
}

// GetSessionID returns the current session ID
func (h *ToolOutputHandler) GetSessionID() string {
	return h.SessionID
}

// CleanupOldFiles deletes files older than the specified maxAge in the output folder
// It scans all session folders and removes files that are older than maxAge
func (h *ToolOutputHandler) CleanupOldFiles(maxAge time.Duration) error {
	if h.OutputFolder == "" {
		return fmt.Errorf("output folder is not set")
	}

	// Check if output folder exists
	if _, err := os.Stat(h.OutputFolder); os.IsNotExist(err) {
		// Folder doesn't exist, nothing to clean
		return nil
	}

	cutoffTime := time.Now().Add(-maxAge)
	var totalDeleted int
	var totalErrors int

	// Walk through all session folders
	err := filepath.Walk(h.OutputFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Continue on errors for individual files/dirs
			return nil
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Check if file is older than cutoff time
		if info.ModTime().Before(cutoffTime) {
			if err := os.Remove(path); err != nil {
				totalErrors++
				// Continue cleaning other files even if one fails
				return nil
			}
			totalDeleted++
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking output folder: %w", err)
	}

	// Clean up empty session directories after file deletion
	h.cleanupEmptyDirectories()

	if totalErrors > 0 {
		return fmt.Errorf("deleted %d files, but encountered %d errors", totalDeleted, totalErrors)
	}

	return nil
}

// CleanupSessionFolder deletes the entire session folder for a given session ID
func (h *ToolOutputHandler) CleanupSessionFolder(sessionID string) error {
	if h.OutputFolder == "" {
		return fmt.Errorf("output folder is not set")
	}

	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}

	sessionFolder := filepath.Join(h.OutputFolder, sessionID)

	// Check if session folder exists
	if _, err := os.Stat(sessionFolder); os.IsNotExist(err) {
		// Folder doesn't exist, nothing to clean
		return nil
	}

	// Remove the entire session folder
	if err := os.RemoveAll(sessionFolder); err != nil {
		return fmt.Errorf("failed to remove session folder: %w", err)
	}

	return nil
}

// CleanupCurrentSessionFolder deletes the current session's folder
func (h *ToolOutputHandler) CleanupCurrentSessionFolder() error {
	return h.CleanupSessionFolder(h.SessionID)
}

// cleanupEmptyDirectories removes empty directories in the output folder
// This is called after file cleanup to remove orphaned session directories
func (h *ToolOutputHandler) cleanupEmptyDirectories() {
	if h.OutputFolder == "" {
		return
	}

	// Walk through directories bottom-up and remove empty ones
	err := filepath.Walk(h.OutputFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip the root output folder itself
		if path == h.OutputFolder {
			return nil
		}

		// Only process directories
		if !info.IsDir() {
			return nil
		}

		// Try to remove directory (will fail if not empty)
		_ = os.Remove(path)
		return nil
	})
	// Ignore errors from cleanup - this is best-effort and shouldn't fail the main operation
	_ = err
}

// isJSONContent checks if the given string is valid JSON
func isJSONContent(content string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(content), &js) == nil
}

// ExtractActualContent removes the "TOOL RESULT for toolname:" prefix and returns the actual content
func ExtractActualContent(prefixedContent string) string {
	// First, try to extract from MCP protocol format: {"type":"text","text":"actual_content"}
	if strings.HasPrefix(prefixedContent, `{"type":"text","text":"`) {
		// Find the closing quote and brace
		startIndex := len(`{"type":"text","text":"`)
		endIndex := strings.LastIndex(prefixedContent, `"}`)
		if endIndex > startIndex {
			// Extract the content between the quotes
			content := prefixedContent[startIndex:endIndex]
			// Unescape the content
			content = strings.ReplaceAll(content, `\"`, `"`)
			content = strings.ReplaceAll(content, `\n`, "\n")
			content = strings.ReplaceAll(content, `\t`, "\t")
			return content
		}
	}

	// Look for the pattern "TOOL RESULT for toolname: "
	prefixPattern := "TOOL RESULT for "
	if strings.HasPrefix(prefixedContent, prefixPattern) {
		// Find the colon after the tool name
		colonIndex := strings.Index(prefixedContent, ": ")
		if colonIndex != -1 {
			// Return everything after ": "
			return prefixedContent[colonIndex+2:]
		}
	}
	// If no prefix found, return the original content (this is now the normal case)
	return prefixedContent
}

// getFileExtension determines the appropriate file extension based on content type
func (h *ToolOutputHandler) getFileExtension(content string) string {
	if isJSONContent(content) {
		return ".json"
	}
	return ".txt"
}
