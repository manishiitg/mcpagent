package mcpagent

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// isContextCanceledError checks if an error is due to context cancellation or deadline exceeded
func isContextCanceledError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "context canceled") ||
		strings.Contains(err.Error(), "context deadline exceeded")
}

func geminiDebugHooksEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCPAGENT_GEMINI_DEBUG_HOOKS"))
	if v == "" {
		return false
	}
	return v == "1" ||
		strings.EqualFold(v, "true") ||
		strings.EqualFold(v, "yes") ||
		strings.EqualFold(v, "on")
}

func geminiHTTPRoutingHooksEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING"))
	if v == "" {
		return false
	}
	return v == "1" ||
		strings.EqualFold(v, "true") ||
		strings.EqualFold(v, "yes") ||
		strings.EqualFold(v, "on")
}

func geminiRestrictToolsPolicyContent() string {
	return `# Gemini CLI tool approvals are handled entirely by the Policy Engine.
[[rule]]
toolName = "mcp_api-bridge_*"
decision = "allow"
priority = 999

[[rule]]
toolName = "google_web_search"
decision = "allow"
priority = 997

[[rule]]
toolName = "*"
decision = "deny"
priority = 996
deny_message = "Use only the declared tools available in this session or google_web_search. Do not switch to blocked built-in tools."
`
}

func (a *Agent) ensureGeminiProjectDirID() string {
	if strings.TrimSpace(a.GeminiProjectDirID) != "" {
		return a.GeminiProjectDirID
	}

	if sessionID := strings.TrimSpace(a.SessionID); sessionID != "" {
		a.GeminiProjectDirID = "session-" + sanitizeGeminiProjectDirID(sessionID)
		return a.GeminiProjectDirID
	}

	projectSuffix, randErr := cryptorand.Int(cryptorand.Reader, big.NewInt(100000))
	if randErr != nil {
		if a.Logger != nil {
			a.Logger.Warn("Failed to generate cryptographic random Gemini project suffix; using timestamp-only fallback", loggerv2.Error(randErr))
		}
		a.GeminiProjectDirID = fmt.Sprintf("%d-00000", time.Now().UnixMilli())
		return a.GeminiProjectDirID
	}
	a.GeminiProjectDirID = fmt.Sprintf("%d-%05d", time.Now().UnixMilli(), projectSuffix.Int64())
	return a.GeminiProjectDirID
}

func sanitizeGeminiProjectDirID(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(raw) {
		allowed := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' ||
			r == '_'
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}

func claudeHTTPRoutingHooksEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING"))
	if v == "" {
		return false
	}
	return v == "1" ||
		strings.EqualFold(v, "true") ||
		strings.EqualFold(v, "yes") ||
		strings.EqualFold(v, "on")
}

func writeClaudeHTTPRoutingHook() (string, error) {
	hooksDir := filepath.Join(os.TempDir(), "claude-code-hooks")
	if err := os.MkdirAll(hooksDir, 0750); err != nil {
		return "", fmt.Errorf("create claude hooks dir: %w", err)
	}

	hookPath := filepath.Join(hooksDir, "enforce-http-tool-routing.py")
	hookScript := `#!/usr/bin/env python3
import json
from pathlib import Path
import sys

ALLOWED = {
    "mcp__api-bridge__execute_shell_command",
    "mcp__api-bridge__diff_patch_workspace_file",
    "mcp__api-bridge__agent_browser",
    "mcp__api-bridge__get_api_spec",
    "WebSearch",
}

raw = sys.stdin.read()
payload = {}
log_path = Path("` + filepath.Join(os.TempDir(), "claude-code-hooks", "pretool.log") + `")

try:
    payload = json.loads(raw) if raw else {}
except Exception:
    payload = {}

tool_name = payload.get("tool_name", "")

with log_path.open("a", encoding="utf-8") as fh:
    fh.write(json.dumps({
        "tool_name": tool_name,
        "tool_input": payload.get("tool_input"),
    }, ensure_ascii=True, sort_keys=True) + "\n")

if tool_name in ALLOWED:
    raise SystemExit(0)

sys.stdout.write(json.dumps({
    "hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny",
        "permissionDecisionReason": (
            "Only mcp__api-bridge__ tools (execute_shell_command, diff_patch_workspace_file, agent_browser, get_api_spec) "
            "and WebSearch are allowed in this Claude Code bridge session. "
            "Use get_api_spec plus execute_shell_command for HTTP-based tool access."
        )
    }
}) + "\n")
`

	if err := os.WriteFile(hookPath, []byte(hookScript), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("write claude hook script: %w", err)
	}
	return hookPath, nil
}

func buildClaudeHTTPRoutingSettings(hookPath string) (string, error) {
	command := fmt.Sprintf("python3 %q", hookPath)
	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []map[string]interface{}{
				{
					"matcher": "*",
					"hooks": []map[string]interface{}{
						{
							"type":    "command",
							"command": command,
							"timeout": 5,
						},
					},
				},
			},
		},
	}

	settingsBytes, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal claude hook settings: %w", err)
	}
	return string(settingsBytes), nil
}

func buildGeminiDebugHooks() map[string]interface{} {
	return map[string]interface{}{
		"BeforeTool": []map[string]interface{}{
			{
				"matcher": "*",
				"hooks": []map[string]interface{}{
					{
						"name":        "log-before-tool",
						"type":        "command",
						"command":     "$GEMINI_PROJECT_DIR/.gemini/hooks/log-before-tool.py",
						"timeout":     5000,
						"description": "Log Gemini BeforeTool payloads to stderr for MCP bridge diagnostics",
					},
				},
			},
		},
	}
}

func writeGeminiHookScripts(projectDir string, debugEnabled bool, enforceHTTPRouting bool) error {
	hooksDir := filepath.Join(projectDir, ".gemini", "hooks")
	if err := os.MkdirAll(hooksDir, 0750); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	if enforceHTTPRouting {
		enforcePath := filepath.Join(hooksDir, "enforce-http-tool-routing.py")
		enforceScript := `#!/usr/bin/env python3
import json
import os
import sys
import datetime

# Allowed tools — both bare names (Gemini built-ins) and MCP-prefixed names
# (bridge tools exposed as mcp_api-bridge_<tool> by the api-bridge MCP server).
# The server name "api-bridge" is hardcoded in BuildBridgeMCPConfig so these are stable.
ALLOWED = {
    # Gemini CLI built-in
    "google_web_search",
    # Bridge tools — bare name (for future direct calls)
    "execute_shell_command",
    "diff_patch_workspace_file",
    "agent_browser",
    "get_api_spec",
    # Bridge tools — MCP-prefixed name (how Gemini CLI presents them from the api-bridge server)
    "mcp_api-bridge_execute_shell_command",
    "mcp_api-bridge_diff_patch_workspace_file",
    "mcp_api-bridge_agent_browser",
    "mcp_api-bridge_get_api_spec",
}

LOG_PATH = os.path.join(os.environ.get("TMPDIR", "/tmp"), "enforce-http-tool-routing.log")

def log(msg):
    try:
        with open(LOG_PATH, "a", encoding="utf-8") as f:
            f.write(f"[{datetime.datetime.utcnow().isoformat()}] {msg}\n")
    except Exception:
        pass

raw = sys.stdin.read()
payload = {}

try:
    payload = json.loads(raw) if raw else {}
except Exception:
    pass

tool_name = payload.get("tool_name", "")
# Strip 'default_api:' prefix that some Gemini models add to MCP tool names
if tool_name.startswith("default_api:"):
    tool_name = tool_name[len("default_api:"):]
mcp_context = payload.get("mcp_context") or {}
server_name = mcp_context.get("server_name")

if tool_name in ALLOWED:
    log(f"BeforeTool ALLOW: tool_name={tool_name!r} server_name={server_name!r}")
    sys.stdout.write("{}\n")
    raise SystemExit(0)

log(f"BeforeTool DENY: tool_name={tool_name!r} server_name={server_name!r}")

reason = (
    "Only execute_shell_command, diff_patch_workspace_file, agent_browser, get_api_spec, and google_web_search are allowed in this Gemini bridge session. "
    "Do not call '" + tool_name + "' directly. "
    "If you need another capability, call get_api_spec to discover the HTTP endpoint and "
    "use execute_shell_command to invoke it via MCP_API_URL/MCP_API_TOKEN."
)

sys.stdout.write(json.dumps({
    "decision": "deny",
    "reason": reason
}) + "\n")
`
		if err := writeExecutableHookScript(enforcePath, enforceScript); err != nil {
			return fmt.Errorf("write enforce http routing hook: %w", err)
		}
	}

	if !debugEnabled {
		return nil
	}

	debugPath := filepath.Join(hooksDir, "log-before-tool.py")
	debugScript := `#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

raw = sys.stdin.read()

try:
    payload = json.loads(raw)
    debug_payload = {
        "hook_event_name": payload.get("hook_event_name"),
        "tool_name": payload.get("tool_name"),
        "original_request_name": payload.get("original_request_name"),
        "mcp_context": payload.get("mcp_context"),
        "tool_input": payload.get("tool_input"),
    }
    project_dir = os.environ.get("GEMINI_PROJECT_DIR", "")
    if project_dir:
        log_path = Path(project_dir) / ".gemini" / "hooks" / "before-tool.log"
        log_path.parent.mkdir(parents=True, exist_ok=True)
        with log_path.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps(debug_payload, ensure_ascii=True, sort_keys=True) + "\n")
    sys.stderr.write(
        "[GEMINI_DEBUG_HOOK BeforeTool] "
        + json.dumps(debug_payload, ensure_ascii=True, sort_keys=True)
        + "\n"
    )
except Exception as exc:
    sys.stderr.write(
        "[GEMINI_DEBUG_HOOK BeforeTool] failed to parse payload: %s\n" % exc
    )
    if raw:
        sys.stderr.write(raw + "\n")

sys.stdout.write("{}\n")
`

	if err := writeExecutableHookScript(debugPath, debugScript); err != nil {
		return fmt.Errorf("write debug hook script: %w", err)
	}
	return nil
}

func writeExecutableHookScript(path, contents string) error {
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0700) //nolint:gosec // hook scripts must be executable
}

// retryOriginalModel handles retry logic for throttling and zero_candidates errors
// Returns: shouldRetry (bool), delay (time.Duration), error
func retryOriginalModel(a *Agent, ctx context.Context, errorType string, attempt, maxRetries int, baseDelay, maxDelay time.Duration, turn int, logger loggerv2.Logger, usage observability.UsageMetrics) (bool, time.Duration, error) {
	// Exponential backoff: 10s, 20s, 40s, 80s, 160s...
	delay := baseDelay * time.Duration(1<<attempt)
	if delay > maxDelay {
		delay = maxDelay
	}

	// Emit retry attempt event with proper model/provider info for UI display
	retryAttemptEvent := events.NewFallbackAttemptEvent(
		turn, attempt+1, maxRetries,
		a.ModelID, string(a.provider), "retry", // Use "retry" phase to distinguish from actual fallbacks
		false, delay, fmt.Sprintf("%s - retrying original model", errorType),
	)
	a.EmitTypedEvent(ctx, retryAttemptEvent)

	var logMsg string
	if errorType == "zero_candidates_error" {
		logMsg = fmt.Sprintf("🔄 [ZERO_CANDIDATES] Retrying original model FIRST (before fallbacks). Waiting %v before retry (attempt %d/%d)...", delay, attempt+1, maxRetries)
	} else {
		logMsg = fmt.Sprintf("🔄 [THROTTLING] Retrying original model FIRST (before fallbacks). Waiting %v before retry (attempt %d/%d)...", delay, attempt+1, maxRetries)
	}
	logger.Info(logMsg)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	// Wait for delay or context cancellation
	select {
	case <-ctx.Done():
		return false, delay, ctx.Err()
	case <-timer.C:
	}

	var retryLogMsg string
	if errorType == "zero_candidates_error" {
		retryLogMsg = fmt.Sprintf("🔄 [ZERO_CANDIDATES] Retrying with original model (turn %d, attempt %d/%d)...", turn, attempt+2, maxRetries)
	} else {
		retryLogMsg = fmt.Sprintf("🔄 [THROTTLING] Retrying with original model (turn %d, attempt %d/%d)...", turn, attempt+2, maxRetries)
	}
	logger.Info(retryLogMsg)
	return true, delay, nil
}

// isMaxTokenError checks if an error is due to reaching maximum token limit
func isMaxTokenError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Exclude context cancellation from max token errors
	if isContextCanceledError(err) {
		return false
	}
	return strings.Contains(msg, "max_token") ||
		strings.Contains(msg, "max tokens") ||
		strings.Contains(msg, "Input is too long") ||
		strings.Contains(msg, "ValidationException") ||
		strings.Contains(msg, "too long")
}

// isQuotaExhaustedError checks if an error is a permanent quota exhaustion (daily/monthly limits)
// that will NOT recover within minutes — skip same-model retries and go straight to fallback.
func isQuotaExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "per_day") ||
		strings.Contains(msg, "per_month") ||
		strings.Contains(msg, "generaterequestsperday") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "quota exceeded for metric") ||
		strings.Contains(msg, "exceeded your current quota") ||
		strings.Contains(msg, "retrydelay:3") || // retryDelay >= 3600s (1+ hour)
		strings.Contains(msg, "retrydelay:4") ||
		strings.Contains(msg, "retrydelay:8") ||
		strings.Contains(msg, "retrydelay:9") ||
		strings.Contains(msg, "hit your usage limit") || // Codex CLI usage exhaustion
		strings.Contains(msg, "you've hit your limit") || // Claude Code usage exhaustion
		strings.Contains(msg, "youve hit your limit") ||
		strings.Contains(msg, "usage limit")
}

// isThrottlingError checks if an error is due to API throttling
func isThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from throttling errors
	if isContextCanceledError(err) {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "ThrottlingException") ||
		strings.Contains(errStr, "Too many tokens") ||
		strings.Contains(errStr, "StatusCode: 429") ||
		strings.Contains(errStr, "API returned unexpected status code: 429") ||
		strings.Contains(errStr, "status code: 429") ||
		strings.Contains(errStr, "status code 429") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "throttled") ||
		strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "model is overloaded") ||
		strings.Contains(errStr, "UNAVAILABLE") ||
		(strings.Contains(errStr, "503") && strings.Contains(errStr, "overloaded"))
}

// isEmptyContentError checks if an error is due to empty content in response
func isEmptyContentError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "MALFORMED_FUNCTION_CALL") {
		return false
	}
	return strings.Contains(msg, "Choice.Content is empty string") ||
		strings.Contains(msg, "empty content error") ||
		strings.Contains(msg, "choice.Content is empty") ||
		strings.Contains(msg, "empty response")
}

// isZeroCandidatesError checks if an error is due to zero candidates returned
func isZeroCandidatesError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "zero candidates") ||
		strings.Contains(msg, "returned zero candidates") ||
		strings.Contains(msg, "no candidates")
}

// isConnectionError checks if an error is due to connection issues
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from connection errors
	if isContextCanceledError(err) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "network") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection lost") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "unexpected EOF")
}

// isStreamError checks if an error is due to streaming issues
func isStreamError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from stream errors
	if isContextCanceledError(err) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "stream error") ||
		strings.Contains(msg, "stream ID") ||
		strings.Contains(msg, "streaming") ||
		strings.Contains(msg, "stream closed") ||
		strings.Contains(msg, "stream interrupted") ||
		strings.Contains(msg, "stream timeout") ||
		strings.Contains(msg, "streaming error")
}

// isInternalError checks if an error is due to internal server issues
func isInternalError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "INTERNAL_ERROR") ||
		strings.Contains(msg, "internal error") ||
		strings.Contains(msg, "server error") ||
		strings.Contains(msg, "unexpected error") ||
		strings.Contains(msg, "received from peer") ||
		strings.Contains(msg, "peer error") ||
		strings.Contains(msg, "internal server error") ||
		strings.Contains(msg, "service error") ||
		strings.Contains(msg, "status 500") ||
		strings.Contains(msg, "status code: 500") ||
		strings.Contains(msg, "status code 500") ||
		strings.Contains(msg, "StatusCode: 500") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "status 502") ||
		strings.Contains(msg, "status code: 502") ||
		strings.Contains(msg, "status code 502") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "status 503") ||
		strings.Contains(msg, "status code: 503") ||
		strings.Contains(msg, "status code 503") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "status 504") ||
		strings.Contains(msg, "status code: 504") ||
		strings.Contains(msg, "status code 504") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "API returned unexpected status code: 5") ||
		strings.Contains(msg, "Bad Gateway") ||
		strings.Contains(msg, "Service Unavailable") ||
		strings.Contains(msg, "Gateway Timeout")
}

// classifyLLMError categorizes the given error into a known LLM error type
func classifyLLMError(err error) string {
	if isMaxTokenError(err) {
		return "max_token_error"
	} else if isQuotaExhaustedError(err) {
		return "quota_exhausted_error"
	} else if isThrottlingError(err) {
		return "throttling_error"
	} else if isZeroCandidatesError(err) {
		return "zero_candidates_error"
	} else if isEmptyContentError(err) {
		return "empty_content_error"
	} else if isConnectionError(err) {
		return "connection_error"
	} else if isStreamError(err) {
		return "stream_error"
	} else if isInternalError(err) {
		return "internal_error"
	} else if isTmuxLossContinuationError(err) {
		return "tmux_loss_error"
	}
	return ""
}

// isTmuxLossContinuationError reports whether err is a failed coding-agent
// session continuation due to tmux loss. The session may still be alive after
// this error, so callers should not surface it as a user-visible error.
func isTmuxLossContinuationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "coding-agent continuation retry after tmux loss failed")
}

// shouldSkipSameModelRetry prefers fast fallback for providers where same-model
// retries are unlikely to improve UX or recover quickly.
func shouldSkipSameModelRetry(provider, errorType string) bool {
	if provider != string(llm.ProviderOpenRouter) {
		return false
	}

	switch errorType {
	case "throttling_error", "internal_error", "connection_error", "stream_error":
		return true
	default:
		return false
	}
}

// streamingManager handles streaming state and goroutine management
type streamingManager struct {
	streamChan        chan llmtypes.StreamChunk
	streamingDone     chan bool
	contentChunkIndex int
	totalChunks       int
	sawTerminal       bool
	suppressEvents    bool
	startTime         time.Time
	turn              int // conversation turn for event emission
	// CLIToolCalls accumulates completed tool call chunks from CLI providers (Gemini CLI,
	// Claude Code, Codex CLI). Used by AskWithHistory to reconstruct conversation history
	// with tool calls that ran inside the CLI subprocess.
	CLIToolCalls []llmtypes.StreamChunk
	// streamDebugFile is an optional per-turn append-only log of every
	// chunk.Content emitted by the adapter. Off by default; toggled on by
	// MCP_AGENT_STREAM_DEBUG=1. Useful when you need to verify "did the
	// model actually emit X" vs. "did the frontend drop X" — the in-memory
	// event store doesn't persist streamed text otherwise.
	streamDebugFile *os.File
}

// startStreaming initializes streaming if enabled and on the first attempt
func (a *Agent) startStreaming(ctx context.Context, attempt int, turn int, opts *[]llmtypes.CallOption) *streamingManager {
	if !a.EnableStreaming || attempt != 0 {
		return nil
	}

	sm := &streamingManager{
		streamChan:     make(chan llmtypes.StreamChunk, 100),
		streamingDone:  make(chan bool, 1),
		startTime:      time.Now(),
		turn:           turn,
		suppressEvents: a.SuppressGenerationStreamingEvents,
	}

	// Per-session/turn raw-stream debug log. Reuses the LOG_AGENT_PROMPTS
	// toggle and the existing logs/agent_prompts/<session>/ directory so
	// the streamed assistant content sits next to its turn's prompt and
	// _conversation.md files — same lifecycle, same cleanup. Captures every
	// chunk.Content emitted by the adapter byte-for-byte, so you can answer
	// "did the model emit X" vs. "did the frontend drop X" without grepping
	// the in-memory event store.
	if os.Getenv("LOG_AGENT_PROMPTS") == "true" && strings.TrimSpace(a.SessionID) != "" {
		sessionDir := strings.ReplaceAll(strings.TrimSpace(a.SessionID), "/", "_")
		dir := filepath.Join("logs", "agent_prompts", sessionDir)
		if err := os.MkdirAll(dir, 0o750); err == nil {
			name := fmt.Sprintf("stream_turn-%03d_attempt-%d_%s.txt", turn, attempt, time.Now().UTC().Format("150405"))
			// Debug-only sink gated by LOG_AGENT_PROMPTS; path is a fixed
			// logs/agent_prompts root + sanitized session id + generated name.
			// #nosec G304 -- not user-controlled file inclusion.
			if f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				fmt.Fprintf(f, "# session=%s turn=%d attempt=%d provider=%s model=%s start=%s\n",
					a.SessionID, turn, attempt, a.provider, a.ModelID, time.Now().UTC().Format(time.RFC3339Nano))
				sm.streamDebugFile = f
			}
		}
	}

	*opts = append(*opts, llmtypes.WithStreamingChan(sm.streamChan))

	if !sm.suppressEvents {
		a.EmitTypedEvent(ctx, &events.StreamingStartEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			Model:         a.ModelID,
			Provider:      string(a.provider),
		})
	}

	go sm.processChunks(ctx, a)
	return sm
}

// processChunks runs in a goroutine to handle incoming streaming chunks
func (sm *streamingManager) processChunks(ctx context.Context, a *Agent) {
	defer func() {
		if sm.streamDebugFile != nil {
			fmt.Fprintf(sm.streamDebugFile, "\n# end %s totalChunks=%d sawTerminal=%t\n",
				time.Now().UTC().Format(time.RFC3339Nano), sm.totalChunks, sm.sawTerminal)
			_ = sm.streamDebugFile.Close()
			sm.streamDebugFile = nil
		}
		sm.streamingDone <- true
	}()

	for chunk := range sm.streamChan {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeContent:
			if chunk.Content != "" {
				sm.contentChunkIndex++
				sm.totalChunks++

				if sm.streamDebugFile != nil {
					fmt.Fprintf(sm.streamDebugFile, "[content idx=%d] %s\n---\n", sm.contentChunkIndex, chunk.Content)
				}

				if !sm.suppressEvents {
					a.EmitTypedEvent(ctx, &events.StreamingChunkEvent{
						BaseEventData: events.BaseEventData{Timestamp: time.Now()},
						Content:       chunk.Content,
						ChunkIndex:    sm.contentChunkIndex,
						IsToolCall:    false,
					})
				}

				if a.StreamingCallback != nil {
					a.StreamingCallback(chunk)
				}
			}

		case llmtypes.StreamChunkTypeTerminal:
			if chunk.Content != "" {
				sm.sawTerminal = true
				sm.contentChunkIndex++
				sm.totalChunks++
				if sm.streamDebugFile != nil {
					fmt.Fprintf(sm.streamDebugFile, "[terminal idx=%d] %s\n---\n", sm.contentChunkIndex, chunk.Content)
				}
				metadata := map[string]interface{}{
					"kind":     "terminal",
					"replace":  true,
					"provider": string(a.provider),
				}
				for key, value := range chunk.Metadata {
					metadata[key] = value
				}

				// Terminal pane snapshots are NOT generation streaming
				// events — they're a separate UX channel that the
				// builder's terminal store consumes. Emit them even
				// when suppressEvents (set via WithGenerationStreamingEvents(false))
				// disables per-token chat-content streaming. Without
				// this, the terminal panel goes empty for every tmux
				// coding-agent call.
				a.EmitTypedEvent(ctx, &events.StreamingChunkEvent{
					BaseEventData: events.BaseEventData{
						Timestamp: time.Now(),
						Metadata:  metadata,
					},
					Content:    chunk.Content,
					ChunkIndex: sm.contentChunkIndex,
					IsToolCall: false,
				})

				if a.StreamingCallback != nil {
					a.StreamingCallback(chunk)
				}
			}

		case llmtypes.StreamChunkTypeToolCallStart:
			// Determine source label from provider
			sourceLabel := string(a.provider)
			if sourceLabel == "" {
				sourceLabel = "cli"
			}
			toolStartEvent := events.NewToolCallStartEventWithCorrelation(
				sm.turn,
				chunk.ToolName,
				events.ToolParams{Arguments: chunk.ToolArgs},
				sourceLabel,
				string(a.TraceID), string(a.TraceID),
			)
			toolStartEvent.ToolCallID = chunk.ToolCallID
			a.EmitTypedEvent(ctx, toolStartEvent)

		case llmtypes.StreamChunkTypeToolCallEnd:
			sourceLabel := string(a.provider)
			if sourceLabel == "" {
				sourceLabel = "cli"
			}
			toolEndEvent := events.NewToolCallEndEventWithTokenUsageAndModel(
				sm.turn,
				chunk.ToolName,
				chunk.ToolResult,   // tool execution result from CLI
				sourceLabel,        // serverName
				chunk.ToolDuration, // duration from start to tool_result
				"",                 // spanID
				0, 0, 0,            // context usage metrics (not available)
				a.ModelID,
			)
			toolEndEvent.ToolCallID = chunk.ToolCallID
			a.EmitTypedEvent(ctx, toolEndEvent)

			// Accumulate for conversation history reconstruction (all CLI providers).
			sm.CLIToolCalls = append(sm.CLIToolCalls, chunk)

			// Forward to StreamingCallback so wrappers (e.g. LLMAgentWrapper) can track
			// completed tool calls for history reconstruction on cancellation.
			if a.StreamingCallback != nil {
				a.StreamingCallback(chunk)
			}
		}
	}
}

// finishStreaming waits for streaming to complete and emits the end event
func (a *Agent) finishStreaming(ctx context.Context, sm *streamingManager, resp *llmtypes.ContentResponse) {
	if sm == nil {
		return
	}

	// If executeLLM failed before calling GenerateContent (e.g. InitializeLLM error),
	// the adapter's deferred close never ran, so close the channel here to unblock
	// processChunks. Use recover to safely handle the "close of closed channel" case.
	func() {
		defer func() { recover() }() //nolint:errcheck
		close(sm.streamChan)
	}()

	<-sm.streamingDone

	// Under production config (suppressEvents=true) we still need to
	// fire the StreamingEndEvent for terminal streams — the terminals
	// store reads it to flip terminal panes from active to inactive.
	// Without this carve-out, cancelled workflow steps leave their
	// terminal entries permanently "active" in the frontend.
	if sm.suppressEvents && !sm.sawTerminal {
		return
	}

	endEvent := &events.StreamingEndEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		TotalChunks:   sm.totalChunks,
		Duration:      time.Since(sm.startTime).String(),
	}
	if sm.sawTerminal {
		endEvent.Metadata = map[string]interface{}{
			"kind":        "terminal",
			"provider":    string(a.provider),
			"duration_ms": time.Since(sm.startTime).Milliseconds(),
		}
	}

	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		genInfo := resp.Choices[0].GenerationInfo
		if genInfo.TotalTokens != nil {
			endEvent.TotalTokens = *genInfo.TotalTokens
		}
		if resp.Choices[0].StopReason != "" {
			endEvent.FinishReason = resp.Choices[0].StopReason
		}
		// Surface per-call tokens + cost on the streaming_end metadata
		// so the terminals store can populate Status.{InputTokens,
		// OutputTokens, CostUSD} without needing to regex-parse a
		// "[done · X in · Y out · $Z]" trailer out of pane content.
		// This is the structured replacement for the synthetic terminal
		// summary line we suppressed for tmux transports.
		if sm.sawTerminal && endEvent.Metadata != nil {
			if genInfo.PromptTokens != nil {
				endEvent.Metadata["input_tokens"] = *genInfo.PromptTokens
			} else if genInfo.InputTokens != nil {
				endEvent.Metadata["input_tokens"] = *genInfo.InputTokens
			}
			if genInfo.CompletionTokens != nil {
				endEvent.Metadata["output_tokens"] = *genInfo.CompletionTokens
			} else if genInfo.OutputTokens != nil {
				endEvent.Metadata["output_tokens"] = *genInfo.OutputTokens
			}
			if genInfo.Additional != nil {
				if cost, ok := genInfo.Additional["cost_usd_estimated"].(float64); ok && cost > 0 {
					endEvent.Metadata["cost_usd_estimated"] = cost
				}
			}
		}
		// Extract provider-specific metadata (Gemini CLI / Claude Code)
		if additional := genInfo.Additional; additional != nil {
			if sm.sawTerminal && endEvent.Metadata != nil {
				if retentionSeconds := terminalRetentionSecondsFromGenerationInfo(additional); retentionSeconds > 0 {
					endEvent.Metadata["terminal_retention_seconds"] = retentionSeconds
				}
				if tmuxSession := terminalTmuxSessionFromGenerationInfo(additional); tmuxSession != "" {
					endEvent.Metadata["tmux_session"] = tmuxSession
				}
			}
			// Gemini CLI metadata
			if model, ok := additional["gemini_model"].(string); ok {
				endEvent.ResolvedModel = model
			}
			if tc, ok := additional["gemini_tool_calls"].(int); ok {
				endEvent.ToolCalls = tc
			}
			// Claude Code metadata
			if model, ok := additional["claude_code_model"].(string); ok && endEvent.ResolvedModel == "" {
				endEvent.ResolvedModel = model
			}
		}
		// Populate cache tokens from CachedContentTokens (set by both adapters)
		if genInfo.CachedContentTokens != nil {
			endEvent.CacheTokens = *genInfo.CachedContentTokens
		}
	}
	a.EmitTypedEvent(ctx, endEvent)
}

func terminalRetentionSecondsFromGenerationInfo(additional map[string]interface{}) int {
	for _, key := range []string{
		"terminal_retention_seconds",
		"claude_code_interactive_retention_seconds",
		"codex_interactive_retention_seconds",
		"gemini_interactive_retention_seconds",
		"cursor_interactive_retention_seconds",
	} {
		if seconds := generationInfoIntValue(additional[key]); seconds > 0 {
			return seconds
		}
	}
	return 0
}

func terminalTmuxSessionFromGenerationInfo(additional map[string]interface{}) string {
	for _, key := range []string{
		"claude_code_session",
		"codex_interactive_session",
		"gemini_interactive_session",
		"cursor_interactive_session",
	} {
		if value := strings.TrimSpace(fmt.Sprint(additional[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func generationInfoIntValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case int32:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(typed))
		return i
	default:
		return 0
	}
}

// getEffectiveLLMConfig returns a unified LLM configuration, compatible with legacy settings
func (a *Agent) getEffectiveLLMConfig() AgentLLMConfiguration {
	var config AgentLLMConfiguration

	// If the new config is populated, use it as base
	if a.LLMConfig.Primary.ModelID != "" && a.LLMConfig.Primary.Provider != "" {
		config = a.LLMConfig
	} else {
		// Otherwise, build from legacy fields
		config = AgentLLMConfiguration{
			Primary: LLMModel{
				Provider: string(a.provider),
				ModelID:  a.ModelID,
				// Note: API Key not easily accessible from legacy Agent struct without introspection
				// but executeLLM will handle this by checking Agent.APIKeys if model.APIKey is nil
			},
			Fallbacks: []LLMModel{},
		}
	}

	// Merge legacy cross-provider fallbacks if available (backward compatibility).
	if a.CrossProviderFallback != nil {
		for _, model := range a.CrossProviderFallback.Models {
			config.Fallbacks = append(config.Fallbacks, LLMModel{
				Provider: a.CrossProviderFallback.Provider,
				ModelID:  model,
			})
		}
	}

	// If no explicit fallbacks were provided, apply provider defaults.
	// This keeps behavior aligned with older initialization paths that used
	// default same-provider and cross-provider fallback env configuration.
	if len(config.Fallbacks) == 0 && config.Primary.Provider != "" {
		defaultFallbackRefs := append([]string{}, llm.GetDefaultFallbackModelsForModel(llm.Provider(config.Primary.Provider), config.Primary.ModelID)...)
		defaultFallbackRefs = append(defaultFallbackRefs, llm.GetCrossProviderFallbackModels(llm.Provider(config.Primary.Provider))...)

		for _, fallbackRef := range defaultFallbackRefs {
			if fallbackModel, ok := parseFallbackModelRef(config.Primary.Provider, fallbackRef); ok {
				config.Fallbacks = append(config.Fallbacks, fallbackModel)
			}
		}
	}

	config.Fallbacks = dedupeFallbacks(config.Fallbacks)
	return config
}

func parseFallbackModelRef(primaryProvider, fallbackRef string) (LLMModel, bool) {
	ref := strings.TrimSpace(fallbackRef)
	if ref == "" {
		return LLMModel{}, false
	}

	slashIdx := strings.Index(ref, "/")
	if slashIdx <= 0 {
		return LLMModel{Provider: primaryProvider, ModelID: ref}, true
	}

	providerCandidate := strings.TrimSpace(ref[:slashIdx])
	modelCandidate := strings.TrimSpace(ref[slashIdx+1:])
	if providerCandidate == "" || modelCandidate == "" {
		return LLMModel{Provider: primaryProvider, ModelID: ref}, true
	}

	// If the prefix is a known provider (e.g., "openai/gpt-5-mini"), treat
	// as cross-provider fallback; otherwise keep as same-provider model ID
	// that happens to contain "/" (e.g., OpenRouter "x-ai/grok-code-fast-1").
	if _, err := llm.ValidateProvider(providerCandidate); err == nil {
		return LLMModel{Provider: providerCandidate, ModelID: modelCandidate}, true
	}

	return LLMModel{Provider: primaryProvider, ModelID: ref}, true
}

func dedupeFallbacks(fallbacks []LLMModel) []LLMModel {
	seen := make(map[string]struct{}, len(fallbacks))
	result := make([]LLMModel, 0, len(fallbacks))

	for _, fallback := range fallbacks {
		provider := strings.TrimSpace(fallback.Provider)
		modelID := strings.TrimSpace(fallback.ModelID)
		if provider == "" || modelID == "" {
			continue
		}

		key := provider + "/" + modelID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		fallback.Provider = provider
		fallback.ModelID = modelID
		result = append(result, fallback)
	}

	return result
}

// executeLLM creates an LLM instance and executes it.
func (a *Agent) executeLLM(ctx context.Context, model LLMModel, messages []llmtypes.MessageContent, opts []llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return a.executeLLMInner(ctx, model, messages, opts, false)
}

func (a *Agent) executeLLMForCodingAgentTransportLaunch(ctx context.Context, model LLMModel, opts []llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Carry the agent's accumulated system prompt through the
	// launch-only contract so the adapter projects its provider-specific
	// rule file (mlp-system.mdc / mlp-system.md / AGENTS.md / GEMINI.md /
	// CLAUDE.md) on resume. Without this, launch-only sends nil messages
	// → adapter's split*SystemPrompt returns empty → prepare*ProjectFiles
	// skips the rule-file write → user sees an empty .cursor/rules/
	// directory mid-conversation.
	if sp := strings.TrimSpace(a.systemPrompt); sp != "" {
		opts = append(opts, llmtypes.WithCodingProviderLaunchSystemPrompt(sp))
	}
	return a.executeLLMInner(ctx, model, nil, opts, true)
}

func (a *Agent) appendAgyCLIIntegrationOptions(opts []llmtypes.CallOption) []llmtypes.CallOption {
	if bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig(); bridgeErr == nil {
		opts = append(opts,
			llm.WithAgyMCPConfig(bridgeConfig),
			llm.WithAgyBridgeOnlyTools(true),
		)
		a.Logger.Info("🌉 [AGY_CLI] Configured MCP bridge through .agents/mcp_config.json with bridge-only hooks")
	} else {
		a.Logger.Warn(fmt.Sprintf("Could not build bridge MCP config for Antigravity CLI (tools may be limited): %v", bridgeErr))
	}
	if a.AgySessionID != "" {
		opts = append(opts, llm.WithAgyResumeSessionID(a.AgySessionID))
	}
	opts = append(opts, llm.WithAgyDangerouslySkipPermissions(true))
	a.Logger.Info("🌉 Using Antigravity CLI in tmux mode with MCP bridge and live input support")
	return opts
}

func (a *Agent) executeLLMInner(ctx context.Context, model LLMModel, messages []llmtypes.MessageContent, opts []llmtypes.CallOption, launchOnly bool) (*llmtypes.ContentResponse, error) {
	// Thread attached skills through opts so CLI transport adapters can
	// project SKILL.md folders to disk via ProjectSkills at session
	// launch. API transports already see the listing in the system
	// prompt via ensureSystemPrompt — they can ignore this metadata.
	// Centralized here so every LLM call (chat, launch-only, retries)
	// carries the same skill set; individual call sites do not need to
	// re-append it.
	if skills := a.attachedSkills; len(skills) > 0 {
		opts = append(opts, llmtypes.WithAttachedSkills(skills))
	}

	// Clone agent-level keys as base (so Azure and Bedrock configs are always available)
	apiKeys := a.APIKeys.Clone()
	if apiKeys == nil {
		apiKeys = &llm.ProviderAPIKeys{}
	}

	// Override with model-specific key if available (for simple API key providers)
	if model.APIKey != nil {
		apiKeys.SetKeyForProvider(llmproviders.Provider(model.Provider), model.APIKey)
	}

	if model.Region != nil && llmproviders.Provider(model.Provider) == llmproviders.ProviderBedrock {
		if apiKeys.Bedrock == nil {
			apiKeys.Bedrock = &llm.BedrockConfig{}
		}
		apiKeys.Bedrock.Region = *model.Region
	}

	// Use model's temperature if available, otherwise fallback to agent's temperature
	temperature := a.Temperature
	if model.Temperature != nil {
		temperature = *model.Temperature
	}

	modelProvider := llm.Provider(model.Provider)
	// Sweep stale coding-agent artifacts BEFORE the adapter projects its
	// own files. Removes leftover .cursor/.claude/.codex/.gemini/.agents/
	// .opencode/ from previous provider switches in this workflow folder,
	// plus stale mlp-system-*.{mdc,md} from sessions that crashed without
	// running their cleanup callbacks. The active provider re-projects
	// immediately inside its adapter's prepare*ProjectFiles. Best-effort;
	// errors here must not block the turn.
	if llm.IsCodingAgentProvider(modelProvider, model.ModelID) {
		CleanupStaleCodingAgentArtifacts(a.CodingAgentWorkingDir, modelProvider)
	}
	opts = a.appendCodingAgentInteractiveOptionsForProvider(opts, modelProvider, model.ModelID)

	llmInstance, err := llm.InitializeLLM(llm.Config{
		Provider:            modelProvider,
		ModelID:             model.ModelID,
		Temperature:         temperature,
		Logger:              a.Logger,
		APIKeys:             apiKeys,
		Tracers:             a.Tracers,
		TraceID:             a.TraceID,
		Context:             ctx,
		ClaudeCodeTransport: a.ClaudeCodeTransport,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// 🔧 CLAUDE CODE INTEGRATION: Inject MCP Config via bridge
	// Claude Code always uses code execution mode — tools are accessed via the
	// mcpbridge stdio binary which forwards calls to the HTTP API endpoints.
	if llmproviders.Provider(model.Provider) == llmproviders.ProviderClaudeCode {
		claudeHTTPHooksEnabled := claudeHTTPRoutingHooksEnabled()

		// Use restricted permissions instead of skipping them entirely
		// Allow our bridge tools and WebSearch to run without prompts.
		// When HTTP tool routing enforcement is enabled, narrow this to the minimal set.
		allowedTools := "mcp__api-bridge__*,WebSearch"
		if claudeHTTPHooksEnabled {
			allowedTools = "mcp__api-bridge__execute_shell_command,mcp__api-bridge__diff_patch_workspace_file,mcp__api-bridge__agent_browser,mcp__api-bridge__get_api_spec,WebSearch"
		}
		opts = append(opts, llm.WithAllowedTools(allowedTools))

		// Force Claude to use our custom tools by disabling its own internal ones
		// We explicitly allow only WebSearch (if desired) and disable all others (Bash, Read, Edit, etc.)
		opts = append(opts, llm.WithClaudeCodeTools("WebSearch"))

		if claudeHTTPHooksEnabled {
			hookPath, hookErr := writeClaudeHTTPRoutingHook()
			if hookErr != nil {
				a.Logger.Warn("Failed to write Claude Code HTTP routing hook", loggerv2.Error(hookErr))
			} else {
				settingsJSON, settingsErr := buildClaudeHTTPRoutingSettings(hookPath)
				if settingsErr != nil {
					a.Logger.Warn("Failed to build Claude Code hook settings", loggerv2.Error(settingsErr))
				} else {
					opts = append(opts, llm.WithClaudeCodeSettings(settingsJSON))
					a.Logger.Info("🪝 Claude Code HTTP tool routing enforcement enabled",
						loggerv2.String("env", "MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING"),
						loggerv2.String("hook_path", hookPath))
				}
			}
		}

		bridgeConfig, err := a.BuildBridgeMCPConfig()
		if err != nil {
			return nil, fmt.Errorf("Claude Code requires the MCP bridge: %w", err)
		}
		opts = append(opts, llm.WithMCPConfig(bridgeConfig))
		a.Logger.Info("🌉 Using MCP bridge for Claude Code tool access via HTTP API")

		// Pass max turns to Claude Code CLI
		if a.MaxTurns > 0 {
			opts = append(opts, llm.WithMaxTurns(a.MaxTurns))
		}

		// Resume existing Claude Code session if available
		if a.ClaudeCodeSessionID != "" {
			opts = append(opts, llm.WithResumeSessionID(a.ClaudeCodeSessionID))
		}

		// Pass effort level from model options
		if model.Options != nil {
			if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
				opts = append(opts, llm.WithClaudeCodeEffort(effort))
				a.Logger.Info(fmt.Sprintf("🧠 [CLAUDE_CODE] Effort level set to: %s", effort))
			}
		}
	}

	// 🔧 GEMINI CLI INTEGRATION: Project settings + MCP bridge
	// Gemini CLI reads .gemini/settings.json from its working directory. We create
	// a temp directory with settings that:
	//   1. Restrict built-in tools via tools.core (only google_web_search allowed)
	//   2. Configure the MCP bridge server via mcpServers
	// The adapter runs `gemini` from that temp dir so these settings take effect.
	if llmproviders.Provider(model.Provider) == llmproviders.ProviderGeminiCLI {
		// No --approval-mode: the Policy Engine TOML handles all tool approval.
		// "allow" decisions auto-approve MCP tools, "deny" blocks built-in tools.
		// Yolo mode bypasses the policy engine entirely, so we must NOT use it.

		a.ensureGeminiProjectDirID()
		// For the workflow main_agent / chat case (IsolatedSessionWorkspace=false
		// AND a workflow-rooted CodingAgentWorkingDir is set), root the project
		// dir inside the workflow folder so GEMINI_PROJECT_DIR survives /tmp
		// wipes and the status line shows a meaningful workspace. Steps stay
		// in /tmp so concurrent runs don't clobber each other's settings.json.
		var projectDir string
		if !a.IsolatedSessionWorkspace && strings.TrimSpace(a.CodingAgentWorkingDir) != "" {
			projectDir = filepath.Join(a.CodingAgentWorkingDir, ".gemini-main")
		} else {
			projectDir = filepath.Join(os.TempDir(), "gemini-cli-project-"+a.GeminiProjectDirID)
		}

		// Build project settings with MCP bridge config.
		// Tool restriction is handled by a per-run --admin-policy TOML file. Do
		// not write tools.exclude here: Gemini CLI deprecates it and warns on
		// startup. Do not install enforcement hooks by default: Gemini warns
		// about project hooks in interactive mode, and Policy Engine can hide
		// denied tools from the model without hook noise.
		settings := map[string]interface{}{}
		debugHooksEnabled := geminiDebugHooksEnabled()
		httpRoutingHooksEnabled := geminiHTTPRoutingHooksEnabled()
		if debugHooksEnabled {
			settings["hooks"] = buildGeminiDebugHooks()
		}
		if debugHooksEnabled {
			a.Logger.Info("🪝 Gemini CLI BeforeTool debug hook enabled",
				loggerv2.String("env", "MCPAGENT_GEMINI_DEBUG_HOOKS"),
				loggerv2.String("project_dir", projectDir))
		}
		if httpRoutingHooksEnabled {
			a.Logger.Info("🔒 Gemini CLI HTTP tool routing policy enabled",
				loggerv2.String("env", "MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING"),
				loggerv2.String("project_dir", projectDir))
		}

		// Build bridge MCP config and merge mcpServers into settings
		bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
		if bridgeErr == nil {
			var bridgeParsed map[string]interface{}
			if json.Unmarshal([]byte(bridgeConfig), &bridgeParsed) == nil {
				if mcpServers, ok := bridgeParsed["mcpServers"]; ok {
					settings["mcpServers"] = mcpServers
				}
			}
		} else {
			a.Logger.Warn("Could not build bridge MCP config for Gemini CLI (tools may be limited)", loggerv2.Error(bridgeErr))
		}

		settingsBytes, _ := json.Marshal(settings)
		opts = append(opts, llm.WithGeminiProjectSettings(string(settingsBytes)))

		// Pre-create a policy file and pass it explicitly with --admin-policy.
		// Workspace .gemini/policies is not enough on current Gemini CLI builds
		// because project-level policies are disabled/non-functional.
		policiesDir := filepath.Join(projectDir, ".gemini", "policies")
		if err := os.MkdirAll(policiesDir, 0750); err != nil {
			a.Logger.Warn("Failed to create Gemini CLI policies directory", loggerv2.Error(err))
		} else {
			policyContent := geminiRestrictToolsPolicyContent()
			policyPath := filepath.Join(policiesDir, "restrict-tools.toml")
			if err := os.WriteFile(policyPath, []byte(policyContent), 0600); err != nil {
				a.Logger.Warn("Failed to write Gemini CLI policy file", loggerv2.Error(err))
			} else {
				opts = append(opts, llm.WithGeminiAdminPolicyPath(policyPath))
				a.Logger.Info(fmt.Sprintf("📋 Wrote Gemini CLI admin policy file to %s", policyPath))
			}
		}
		if debugHooksEnabled {
			if err := writeGeminiHookScripts(projectDir, true, false); err != nil {
				a.Logger.Warn("Failed to write Gemini CLI hook scripts", loggerv2.Error(err))
			} else {
				a.Logger.Info("🪝 Gemini CLI BeforeTool debug hook script ready",
					loggerv2.String("path", filepath.Join(projectDir, ".gemini", "hooks", "log-before-tool.py")))
			}
		}

		a.Logger.Info("🌉 Using Gemini CLI with project settings (MCP bridge configured, policy engine active)")

		// Resume existing Gemini session if available
		if a.GeminiSessionID != "" {
			opts = append(opts, llm.WithGeminiResumeSessionID(a.GeminiSessionID))
		}

		// When a coding-agent working directory is configured, it remains the
		// source of truth for MCP shell cwd and caller-visible workspace access.
		// The Gemini adapter may still launch the CLI from an isolated project
		// settings dir because Gemini discovers .gemini/settings.json from cwd.
		opts = append(opts, llm.WithGeminiProjectDirID(a.GeminiProjectDirID))
		if !a.IsolatedSessionWorkspace && strings.TrimSpace(a.CodingAgentWorkingDir) != "" {
			// Main_agent / chat: tell the adapter to use the workflow-rooted
			// project dir we computed above instead of the /tmp default.
			opts = append(opts, llm.WithGeminiProjectDirAbsolute(projectDir))
		}
		if strings.TrimSpace(a.CodingAgentWorkingDir) != "" {
			opts = append(opts, llm.WithGeminiWorkingDir(a.CodingAgentWorkingDir))
			a.Logger.Info(fmt.Sprintf("[GEMINI_CLI] Using working dir: %s, project dir: %s, project dir ID: %s (session: %s)", a.CodingAgentWorkingDir, projectDir, a.GeminiProjectDirID, a.GeminiSessionID))
		} else {
			a.Logger.Info(fmt.Sprintf("[GEMINI_CLI] Using project dir ID: %s (session: %s)", a.GeminiProjectDirID, a.GeminiSessionID))
		}
	}

	// 🔧 CODEX CLI INTEGRATION: MCP bridge + disable shell + auto-approve
	if llmproviders.Provider(model.Provider) == llmproviders.ProviderCodexCLI {
		// Disable shell tool so Codex only uses MCP bridge tools
		opts = append(opts, llm.WithCodexDisableShellTool())
		// Auto-approve all tool calls (no interactive prompts)
		opts = append(opts, llm.WithCodexApprovalPolicy("never"))
		// Explicitly pin sandbox=workspace-write so apply_patch (which
		// is NOT covered by --disable feature flags — see
		// codexBridgeOnlyDisabledFeatures in
		// multi-llm-provider-go/pkg/adapters/codexcli/options.go) is
		// still confined to the session's cwd. When the workflow-step
		// isolation flag is set, cwd is an os.MkdirTemp dir so
		// apply_patch can only write to the tmp dir and never reach
		// the user's actual workflow files. For chat mode (no
		// isolation), cwd is the user's workspace dir so apply_patch
		// edits the user's files — the desired chat UX. Codex's
		// implicit default under approval=never happens to be
		// workspace-write today, but we set it explicitly so the
		// isolation guarantee doesn't silently regress if codex's
		// default changes.
		opts = append(opts, llm.WithCodexSandbox("workspace-write"))
		if a.CodexSessionID != "" {
			opts = append(opts, llm.WithCodexResumeSessionID(a.CodexSessionID))
		}

		// Build MCP bridge config and pass as Codex CLI config overrides
		// Codex CLI uses config.toml format: mcp_servers.<name>.command, .args, .env
		bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
		if bridgeErr == nil {
			var bridgeParsed map[string]interface{}
			if json.Unmarshal([]byte(bridgeConfig), &bridgeParsed) == nil {
				if mcpServers, ok := bridgeParsed["mcpServers"].(map[string]interface{}); ok {
					if apiBridge, ok := mcpServers["api-bridge"].(map[string]interface{}); ok {
						var configOverrides []string

						// Set the bridge command
						if cmd, ok := apiBridge["command"].(string); ok {
							configOverrides = append(configOverrides, fmt.Sprintf("mcp_servers.api-bridge.command=%q", cmd))
						}

						// Set environment variables for the bridge
						if envMap, ok := apiBridge["env"].(map[string]interface{}); ok {
							for k, v := range envMap {
								if vStr, ok := v.(string); ok {
									configOverrides = append(configOverrides, fmt.Sprintf("mcp_servers.api-bridge.env.%s=%q", k, vStr))
								}
							}
						}

						// Set a generous tool timeout so long-running shell commands
						// (web searches, sub-agent calls, analysis scripts) are not
						// killed by the Codex CLI default of 60s.
						configOverrides = append(configOverrides, "mcp_servers.api-bridge.tool_timeout_sec=5400")

						if len(configOverrides) > 0 {
							opts = append(opts, llm.WithCodexConfigOverrides(configOverrides))
							a.Logger.Info(fmt.Sprintf("🌉 [CODEX_CLI] Configured MCP bridge with %d config overrides", len(configOverrides)))
						}
					}
				}
			}
		} else {
			a.Logger.Warn(fmt.Sprintf("Could not build bridge MCP config for Codex CLI (tools may be limited): %v", bridgeErr))
		}

		// Pass reasoning effort from model options
		if model.Options != nil {
			if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
				opts = append(opts, llm.WithCodexReasoningEffort(effort))
				a.Logger.Info(fmt.Sprintf("🧠 [CODEX_CLI] Reasoning effort set to: %s", effort))
			}
		}

		a.Logger.Info("🌉 Using Codex CLI with shell disabled, MCP bridge, and auto-approval")
	}

	// 🔧 CURSOR CLI INTEGRATION: MCP bridge + tmux live input
	if llmproviders.Provider(model.Provider) == llmproviders.ProviderCursorCLI {
		denyBuiltinsEnabled := false
		bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
		if bridgeErr == nil {
			opts = append(opts, llm.WithCursorMCPConfig(bridgeConfig))
			// --approve-mcps auto-accepts cursor's "approve this MCP server?"
			// TUI dialog so the FIRST bridge tool call does not stall waiting
			// for a human to click through. Required whenever WithCursorMCPConfig
			// is set in a headless context.
			opts = append(opts, llm.WithCursorApproveMCPs())
			// WithCursorDenyBuiltinTools installs a per-session
			// .cursor/hooks.json that denies cursor's built-in
			// Shell/Read/Edit/Write/etc. tools at the hook layer,
			// forcing the agent to route every tool call through the
			// MCP bridge we just configured. Mirrors what
			// appendAgyCLIIntegrationOptions does with
			// WithAgyBridgeOnlyTools(true) — same effect, different
			// CLI's hook convention. Only enabled alongside the bridge
			// config so we never deny built-ins without a working MCP
			// fallback.
			opts = append(opts, llm.WithCursorDenyBuiltinTools(true))
			denyBuiltinsEnabled = true
			a.Logger.Info("🌉 [CURSOR_CLI] Configured MCP bridge through .cursor/mcp.json with deny-builtin hooks")
		} else {
			a.Logger.Warn(fmt.Sprintf("Could not build bridge MCP config for Cursor CLI (tools may be limited): %v", bridgeErr))
		}

		// --force (= --yolo) puts cursor in auto-approve-everything mode,
		// which bypasses the .cursor/hooks.json deny verdicts we install
		// above. The two are mutually exclusive — with --force, cursor
		// never consults the hook so built-in Shell/Read run unimpeded.
		// Only pass --force when deny-builtins is OFF (no bridge → no
		// hooks → fall back to yolo to avoid per-call approval stalls).
		// Cursor's default agent mode is used either way (no WithCursorMode
		// call) because --mode ask/plan put cursor in a conversational
		// stance that refuses natural writes with "Switch to Agent mode".
		// See coding_agent_options.go.
		if !denyBuiltinsEnabled {
			opts = append(opts, llm.WithCursorForce())
			a.Logger.Info("🌉 Using Cursor CLI in tmux mode with MCP bridge and live input support (--force yolo: bridge unavailable)")
		} else {
			a.Logger.Info("🌉 Using Cursor CLI in tmux mode with MCP bridge and deny-builtin hooks (no --force; hooks gate built-ins)")
		}
	}

	// 🔧 ANTIGRAVITY CLI INTEGRATION: MCP bridge + tmux live input.
	if llmproviders.Provider(model.Provider) == llmproviders.ProviderAgyCLI {
		opts = a.appendAgyCLIIntegrationOptions(opts)
	}

	// 🔧 OPENCODE CLI INTEGRATION: MCP bridge + structured JSON transport.
	// All sub-provider tiles (opencode-cli-kimi / -deepseek / -qwen /
	// -minimax / -glm / -free) share this path; the sub-provider scope
	// itself is baked into the adapter via NewOpenCodeCLIAdapterForSub-
	// Provider during InitializeLLM.
	if llmproviders.IsOpenCodeCLIProvider(llmproviders.Provider(model.Provider)) {
		bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
		if bridgeErr == nil {
			opts = append(opts, llm.WithOpenCodeMCPConfig(bridgeConfig))
			a.Logger.Info("🌉 [OPENCODE_CLI] Configured MCP bridge through opencode.jsonc")
		} else {
			a.Logger.Warn(fmt.Sprintf("Could not build bridge MCP config for OpenCode CLI (tools may be limited): %v", bridgeErr))
		}
		if model.Options != nil {
			if agent, ok := model.Options["agent"].(string); ok && agent != "" {
				opts = append(opts, llm.WithOpenCodeAgent(agent))
				a.Logger.Info(fmt.Sprintf("🤖 [OPENCODE_CLI] Agent set to: %s", agent))
			}
		}
		// Sub-provider tiles may also carry per-call overrides for the
		// sub-provider scope. The adapter inherits its construction-
		// time defaults when no override is present, so passing these
		// only matters when a dispatcher wants to swap credentials at
		// runtime (e.g. multi-tenant proxy).
		if model.Options != nil {
			if id, ok := model.Options["opencode_sub_provider_id"].(string); ok && id != "" {
				opts = append(opts, llm.WithOpenCodeSubProvider(id))
			}
			if rawKeys, ok := model.Options["opencode_sub_provider_api_keys"].(map[string]string); ok && len(rawKeys) > 0 {
				opts = append(opts, llm.WithOpenCodeSubProviderAPIKeys(rawKeys))
			}
		}
		if llmproviders.IsOpenCodeSubProvider(llmproviders.Provider(model.Provider)) {
			a.Logger.Info(fmt.Sprintf("🌉 Using OpenCode CLI sub-provider tile %s with MCP bridge", model.Provider))
		} else {
			a.Logger.Info("🌉 Using OpenCode CLI structured JSON mode with MCP bridge")
		}
	}

	// Apply model options for all providers (reasoning_effort, thinking_level, etc.)
	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" && llmproviders.Provider(model.Provider) != llmproviders.ProviderCodexCLI && llmproviders.Provider(model.Provider) != llmproviders.ProviderCursorCLI && llmproviders.Provider(model.Provider) != llmproviders.ProviderOpenCodeCLI {
			opts = append(opts, llmtypes.WithReasoningEffort(effort))
		}
		if level, ok := model.Options["thinking_level"].(string); ok && level != "" {
			opts = append(opts, llmtypes.WithThinkingLevel(level))
		}
		if budget, ok := numericOption(model.Options["thinking_budget"]); ok && budget > 0 {
			opts = append(opts, llmtypes.WithThinkingBudget(int(budget)))
		}
		// Sampling controls. Provider-agnostic; adapters only forward
		// to the wire when they accept the field. JSON unmarshals
		// numeric values into float64, so we accept either int or
		// float types via numericOption.
		if topP, ok := numericOption(model.Options["top_p"]); ok && topP > 0 {
			opts = append(opts, llmtypes.WithTopP(topP))
		}
		if topK, ok := numericOption(model.Options["top_k"]); ok && topK > 0 {
			opts = append(opts, llmtypes.WithTopK(int(topK)))
		}
		if stops, ok := stringSliceOption(model.Options["stop_sequences"]); ok && len(stops) > 0 {
			opts = append(opts, llmtypes.WithStopSequences(stops))
		}
	}

	if continuationHandle, ok := a.codingProviderContinuationHandleForModel(modelProvider, model.ModelID); ok {
		if launchOnly {
			transport := strings.TrimSpace(continuationHandle.Transport)
			if transport == "" {
				if contract, ok := llm.GetCodingAgentProviderContract(modelProvider, model.ModelID); ok {
					transport = string(contract.Transport)
				}
			}
			a.Logger.Info(fmt.Sprintf("🔁 [CODING_AGENT_CONTINUATION] Starting %s transport session (%s) for native session %s", model.Provider, transport, continuationHandle.NativeSessionID))
			return llm.StartCodingAgentTransportSession(ctx, llmInstance, continuationHandle, opts...)
		}
		latestMessage, msgOK := latestHumanMessageTextForProviderContinuation(messages)
		if !msgOK {
			return nil, fmt.Errorf("cannot continue coding-agent session: latest human message not found")
		}
		// Carry the system prompt through the continuation path too —
		// without it, ContinueCodingAgentSession builds messages with
		// only a Human entry and the adapter's split*SystemPrompt
		// returns empty, causing prepare*ProjectFiles to skip the rule
		// file and the "launch configuration changed" guard to recycle
		// the tmux session mid-chat.
		continuationOpts := opts
		if sp := strings.TrimSpace(a.systemPrompt); sp != "" {
			continuationOpts = append(continuationOpts, llmtypes.WithCodingProviderLaunchSystemPrompt(sp))
		}
		a.Logger.Info(fmt.Sprintf("🔁 [CODING_AGENT_CONTINUATION] Continuing %s with native session %s", model.Provider, continuationHandle.NativeSessionID))
		return llm.ContinueCodingAgentSession(ctx, llmInstance, continuationHandle, latestMessage, continuationOpts...)
	}

	return llmInstance.GenerateContent(ctx, messages, opts...)
}

// StartCodingAgentTransportSession starts or reacquires the agent's current
// launchable coding-agent transport without sending a user message. Terminal
// chunks are emitted through the agent's normal event listeners, but no
// streaming_end event is emitted because this is an idle terminal warmup rather
// than a completed generation turn.
func (a *Agent) StartCodingAgentTransportSession(ctx context.Context) (*llmtypes.CodingProviderSessionHandle, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	contract, ok := llm.GetCodingAgentProviderContract(a.provider, a.ModelID)
	if !ok {
		return nil, fmt.Errorf("agent provider %s/%s is not a coding-agent provider", a.provider, a.ModelID)
	}
	if a.ForceStructuredCodingAgent || contract.Transport != llm.CodingAgentTransportTmux {
		return nil, fmt.Errorf("agent provider %s/%s does not expose a launchable terminal transport (%s)", a.provider, a.ModelID, contract.Transport)
	}
	primary := a.getEffectiveLLMConfig().Primary
	if strings.TrimSpace(primary.Provider) == "" {
		primary.Provider = string(a.provider)
	}
	if strings.TrimSpace(primary.ModelID) == "" {
		primary.ModelID = a.ModelID
	}

	var opts []llmtypes.CallOption
	sm := a.startStreaming(ctx, 0, 0, &opts)
	resp, err := a.executeLLMForCodingAgentTransportLaunch(ctx, primary, opts)
	a.drainStreamingWithoutEnd(sm)
	if err != nil {
		return nil, err
	}
	a.updateCodingProviderSessionHandleFromResponse(resp)
	if handle := a.CurrentAgentSessionHandle(); handle != nil && !handle.Provider.Empty() {
		providerHandle := handle.Provider
		return &providerHandle, nil
	}
	return nil, fmt.Errorf("coding-agent transport session started without provider handle")
}

// StartCodingAgentTmuxSession preserves the older tmux-specific entry point for
// callers that have not moved to the transport-level API.
func (a *Agent) StartCodingAgentTmuxSession(ctx context.Context) (*llmtypes.CodingProviderSessionHandle, error) {
	return a.StartCodingAgentTransportSession(ctx)
}

func (a *Agent) drainStreamingWithoutEnd(sm *streamingManager) {
	if sm == nil {
		return
	}
	func() {
		defer func() { recover() }() //nolint:errcheck
		close(sm.streamChan)
	}()
	<-sm.streamingDone
}

func latestHumanMessageTextForProviderContinuation(messages []llmtypes.MessageContent) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llmtypes.ChatMessageTypeHuman {
			continue
		}
		var parts []string
		for _, part := range messages[i].Parts {
			switch typed := part.(type) {
			case llmtypes.TextContent:
				if strings.TrimSpace(typed.Text) != "" {
					parts = append(parts, typed.Text)
				}
			case *llmtypes.TextContent:
				if typed != nil && strings.TrimSpace(typed.Text) != "" {
					parts = append(parts, typed.Text)
				}
			case string:
				if strings.TrimSpace(typed) != "" {
					parts = append(parts, typed)
				}
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n"))
		if text != "" {
			return text, true
		}
		return "", false
	}
	return "", false
}

// GenerateContentWithRetry handles LLM generation with robust retry logic and tiered fallback
func GenerateContentWithRetry(a *Agent, ctx context.Context, messages []llmtypes.MessageContent, opts []llmtypes.CallOption, turn int) (*llmtypes.ContentResponse, observability.UsageMetrics, error) {
	logger := getLogger(a)
	logger.Info(fmt.Sprintf("🔄 [DEBUG] GenerateContentWithRetry START - Messages: %d, Options: %d, Turn: %d", len(messages), len(opts), turn))

	maxRetries := 5
	if env := os.Getenv("LLM_MAX_RETRIES"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 {
			maxRetries = val
		}
	}

	maxRetriesZeroCandidates := 3 // Limit retries for zero_candidates errors to 3 before fallback
	maxRetriesEmptyContent := 2   // Empty-content errors are partly structural; 2 retries rides out transient hiccups without burning cost when failure is permanent

	baseDelaySeconds := 10
	if env := os.Getenv("LLM_RETRY_BASE_DELAY_SECONDS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 {
			baseDelaySeconds = val
		}
	}
	baseDelay := time.Duration(baseDelaySeconds) * time.Second

	maxDelaySeconds := 300 // 5 minutes
	if env := os.Getenv("LLM_RETRY_MAX_DELAY_SECONDS"); env != "" {
		if val, err := strconv.Atoi(env); err == nil && val > 0 {
			maxDelaySeconds = val
		}
	}
	maxDelay := time.Duration(maxDelaySeconds) * time.Second
	var lastErr error
	var usage observability.UsageMetrics

	// Get effective configuration (supports new and legacy)
	llmConfig := a.getEffectiveLLMConfig()

	// Build list of models to try: Primary + Fallbacks, skipping permanently quota-exhausted models.
	allModels := append([]LLMModel{llmConfig.Primary}, llmConfig.Fallbacks...)
	var modelsToTry []LLMModel
	for _, m := range allModels {
		key := m.Provider + "/" + m.ModelID
		if a.quotaExhaustedModels[key] {
			logger.Info(fmt.Sprintf("⏭️ [QUOTA_SKIP] Skipping permanently exhausted model %s (remembered from prior turn)", key))
			continue
		}
		modelsToTry = append(modelsToTry, m)
	}
	if len(modelsToTry) == 0 {
		return nil, usage, fmt.Errorf("all LLMs failed (primary + %d fallbacks): all models are quota-exhausted", len(llmConfig.Fallbacks))
	}

	generationStartTime := time.Now()

	// Emit start event
	a.EmitTypedEvent(ctx, &events.LLMGenerationWithRetryEvent{
		BaseEventData: events.BaseEventData{Timestamp: generationStartTime},
		Turn:          turn,
		MaxRetries:    maxRetries,
		PrimaryModel:  llmConfig.Primary.ModelID,
		CurrentLLM:    llmConfig.Primary.ModelID,
		// SameProviderFallbacks:  sameProviderFallbacks, // Deprecated/merged
		// CrossProviderFallbacks: crossProviderFallbacks, // Deprecated/merged
		Provider:  llmConfig.Primary.Provider,
		Operation: "llm_generation_with_fallback",
		Status:    "started",
	})

	// Iterate through models
	for modelIndex, model := range modelsToTry {
		isFallback := modelIndex > 0
		if isFallback {
			logger.Info(fmt.Sprintf("🔄 Trying fallback %d/%d: %s/%s",
				modelIndex, len(llmConfig.Fallbacks), model.Provider, model.ModelID))

			// Emit fallback model used event
			fallbackEvent := events.NewFallbackModelUsedEvent(turn, llmConfig.Primary.ModelID, model.ModelID, model.Provider, "fallback_chain", time.Since(generationStartTime))
			a.EmitTypedEvent(ctx, fallbackEvent)

			// Temporarily update agent's model ID for consistent event logging
			// This is important because EmitTypedEvent uses a.ModelID in some places
			// We revert it later if we fail, or keep it if we succeed and want to stick to it?
			// The original logic kept it on success.
			a.ModelID = model.ModelID
			a.provider = llm.Provider(model.Provider)
		}

		// Try executing with retries (throttling/transient error handling)
		for attempt := 0; attempt < maxRetries; attempt++ {
			if ctx.Err() != nil {
				return nil, usage, a.handleContextCancellation(ctx, turn, generationStartTime)
			}

			// Create a copy of options for this attempt
			currentOpts := make([]llmtypes.CallOption, len(opts))
			copy(currentOpts, opts)

			// Start streaming (only on first attempt of primary model, or maybe disable for fallbacks?)
			// Original logic: streaming enabled for primary, disabled for fallbacks in loop
			// Here we can enable it if the agent supports it, but fallback logic usually disables it for simplicity
			// For now, let's keep it enabled if it's the first model, or if we want streaming on fallbacks too
			// The original code passed `opts` to fallback generation which might include streaming channel?
			// Actually `startStreaming` modifies `currentOpts` to add the channel.
			// If we are in fallback, we probably shouldn't use the SAME channel if the previous one closed?
			// `startStreaming` creates a NEW channel every time it's called.
			// So streaming on fallback is fine if the frontend can handle it.
			// However, the original code used "non-streaming approach for all agents during fallback".
			// Let's stick to that for safety: only stream on primary model (modelIndex == 0).
			// Enable streaming for all models (primary + fallback) so tool_call events are emitted
			sm := a.startStreaming(ctx, attempt, turn, &currentOpts)

			// Execute LLM
			resp, err := a.executeLLM(ctx, model, messages, currentOpts)

			a.finishStreaming(ctx, sm, resp)

			// After finishStreaming, processChunks has fully drained — sm.CLIToolCalls is
			// complete. Attach the collected tool calls to the response so AskWithHistory
			// can reconstruct a proper conversation history for CLI providers (Gemini CLI,
			// Claude Code, Codex CLI) where tools run inside the subprocess.
			if sm != nil && len(sm.CLIToolCalls) > 0 && resp != nil && len(resp.Choices) > 0 {
				choice := resp.Choices[0]
				if choice.GenerationInfo == nil {
					choice.GenerationInfo = &llmtypes.GenerationInfo{}
				}
				if choice.GenerationInfo.Additional == nil {
					choice.GenerationInfo.Additional = make(map[string]interface{})
				}
				if histJSON, err2 := json.Marshal(sm.CLIToolCalls); err2 == nil {
					choice.GenerationInfo.Additional["cli_tool_call_chunks"] = string(histJSON)
				}
			}

			if err == nil {
				usage = extractUsageMetricsWithMessages(resp, messages)

				if isFallback {
					// Emit fallback success event
					fallbackAttemptEvent := events.NewFallbackAttemptEvent(
						turn, modelIndex, len(llmConfig.Fallbacks),
						model.ModelID, model.Provider, "fallback_chain",
						true, time.Since(generationStartTime), "",
					)
					a.EmitTypedEvent(ctx, fallbackAttemptEvent)

					// Emit model change event to track the permanent model change
					modelChangeEvent := events.NewModelChangeEvent(turn, llmConfig.Primary.ModelID, model.ModelID, "fallback_success", model.Provider, time.Since(generationStartTime))
					a.EmitTypedEvent(ctx, modelChangeEvent)

					// Update agent's config to use this working model as primary for future calls?
					// The original code did: a.ModelID = fallbackModelID; a.LLM = fallbackLLM
					// For this refactor, we are not storing the LLM instance permanently for fallbacks in the same way,
					// but we should probably update a.ModelID and a.provider for consistency.
					// We already did that at the start of the loop.
					// We should also update LLMConfig.Primary to this model to avoid retrying failed primary next turn?
					// That's a behavior change. Let's strictly follow the "permanent update" behavior of original code.
					a.ModelID = model.ModelID
					a.provider = llm.Provider(model.Provider)
					// Note: a.LLM is not updated here because we create it on the fly in executeLLM.
					// If we want to persist it, we'd need to re-initialize a.LLM.
					// But since we use executeLLM now, we don't strictly rely on a.LLM for generation anymore in this function.
					// However, other parts of Agent might use a.LLM (e.g. token counting metadata).
					// Ideally we should update a.LLM.
					// For now, let's leave a.LLM as is or update it if possible.
					// Re-initializing a.LLM here might be expensive or unnecessary if we always use executeLLM.
				} else {
					// Primary succeeded
					logger.Info(fmt.Sprintf("✅ Primary LLM succeeded: %s/%s", model.Provider, model.ModelID))
				}

				return resp, usage, nil
			}

			// Handle context cancellation specifically
			if isContextCanceledError(err) || ctx.Err() != nil {
				return nil, usage, a.handleContextCancellation(ctx, turn, generationStartTime)
			}

			errorType := classifyLLMError(err)
			lastErr = err

			// Special handling for retrying SAME model (throttling/zero candidates/internal errors)
			// For zero_candidates errors: limit to 3 retries before fallback
			// For throttling/internal errors: use full 5 retries
			shouldRetrySameModel := false
			if shouldSkipSameModelRetry(model.Provider, errorType) {
				logger.Info(fmt.Sprintf("⏭️ [FAST_FALLBACK] Skipping same-model retry for %s/%s on %s; moving directly to fallback chain",
					model.Provider, model.ModelID, errorType))
			} else if errorType == "quota_exhausted_error" {
				// Permanent quota exhaustion (daily/monthly) — retrying same model is pointless.
				// Remember this model so future turns skip it immediately.
				if a.quotaExhaustedModels == nil {
					a.quotaExhaustedModels = make(map[string]bool)
				}
				key := model.Provider + "/" + model.ModelID
				a.quotaExhaustedModels[key] = true
				logger.Info(fmt.Sprintf("🚫 [QUOTA_EXHAUSTED] Daily/permanent quota exceeded for %s — marked as exhausted for remaining turns", key))
				break
			} else if errorType == "zero_candidates_error" {
				// Zero candidates: retry up to 3 times (attempts 0, 1, 2 = 3 retries total)
				if attempt < maxRetriesZeroCandidates-1 {
					shouldRetrySameModel = true
				} else {
					logger.Info(fmt.Sprintf("🔄 [ZERO_CANDIDATES] Reached max retries (%d) for zero_candidates error, moving to fallback models", maxRetriesZeroCandidates))
					// Break immediately - don't continue the loop
					logger.Warn(fmt.Sprintf("❌ Model failed after %d retries: %s/%s - %v", maxRetriesZeroCandidates, model.Provider, model.ModelID, err))
					break // Break retry loop, proceed to next model
				}
			} else if errorType == "throttling_error" || errorType == "internal_error" || errorType == "connection_error" || errorType == "stream_error" {
				// Throttling/internal/connection/stream errors: retry up to 5 times (transient)
				if attempt < maxRetries-1 {
					shouldRetrySameModel = true
				}
			} else if errorType == "empty_content_error" {
				// Empty-content errors include both transient cases (Gemini CLI
				// status=error mid-stream with no detail, e.g. backend 5xx) and
				// non-transient ones (context too large, safety filter). Retry
				// up to 2 times — enough to ride out a transient hiccup without
				// burning extra cost when the failure is structural.
				if attempt < maxRetriesEmptyContent-1 {
					shouldRetrySameModel = true
				}
			}

			if shouldRetrySameModel {
				// Use error-type-specific retry caps.
				retryLimit := maxRetries
				if errorType == "zero_candidates_error" {
					retryLimit = maxRetriesZeroCandidates
				} else if errorType == "empty_content_error" {
					retryLimit = maxRetriesEmptyContent
				}
				shouldRetry, _, retryErr := retryOriginalModel(a, ctx, errorType, attempt, retryLimit, baseDelay, maxDelay, turn, logger, usage)
				if retryErr != nil {
					return nil, usage, retryErr
				}
				if shouldRetry {
					continue // Retry same model
				}
			}

			// If not a retryable error on same model, or max retries reached:
			// Break inner loop to try next model in fallback list
			logger.Warn(fmt.Sprintf("❌ Model failed: %s/%s - %v", model.Provider, model.ModelID, err))

			// Emit failure event for this model
			if isFallback {
				failureEvent := events.NewFallbackAttemptEvent(
					turn, modelIndex, len(llmConfig.Fallbacks),
					model.ModelID, model.Provider, "fallback_chain",
					false, time.Since(generationStartTime), err.Error(),
				)
				a.EmitTypedEvent(ctx, failureEvent)
			}

			break // Break retry loop, proceed to next model
		}
	}

	// If all models failed
	return nil, usage, fmt.Errorf("all LLMs failed (primary + %d fallbacks): %w", len(llmConfig.Fallbacks), lastErr)
}

// handleContextCancellation emits cancellation event and returns the error
func (a *Agent) handleContextCancellation(ctx context.Context, turn int, startTime time.Time) error {
	err := ctx.Err()
	if err == nil {
		err = context.Canceled
	}
	a.EmitTypedEvent(ctx, events.NewContextCancelledEvent(turn, err.Error(), time.Since(startTime)))
	return err
}
