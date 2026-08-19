package mcpagent

import (
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var debugToolCallOriginFallbackLogger = loggerv2.NewDefault()

// PLAT-149. Two mechanisms report the same bridge tool call under
// different identities: a toolcalllog-backed HTTP hook (agent_go/pkg/agentwrapper,
// reliable, provider-agnostic, confirmed by reading mcpagent/executor's
// HandleCustomExecute end to end) and a second mechanism carrying the
// provider's own tool_use/call id (Claude-format toolu_XXXXX or Codex-format
// call_XXXX) that drops roughly 10% of results.
//
// The second mechanism's construction site was not found by reading source.
// Exhaustively ruled out, each independently confirmed:
//   - claudecode_interactive_adapter.go's StreamChan sends: exactly four chunk
//     kinds (Content, Terminal, StatusLine, the opt-in transcript one) — zero
//     tool-call sends, confirmed by grepping both the chunk-type constant AND
//     the raw `streamChan <-` send syntax.
//   - codexcli_interactive_adapter.go / claudecode_interactive_adapter.go: no
//     literal StreamChunkTypeToolCallStart/End construction anywhere.
//   - streamClaudeTranscript / codex's transcript-stream equivalent: real,
//     provider-native ids, but opt-in via WithStreamTranscript and, as far as
//     could be confirmed by grep, only ever called by family-server.
//   - tool_registry.go's observedDirectToolExecutor: explicitly skips
//     emission whenever isTurnInFlight(), true for ordinary step execution.
//   - parallel_tool_execution.go / llm_generation.go's agentic-loop tool
//     dispatch: plausible in shape, ruled out because useCodeExecutionMode is
//     auto-enabled for both Claude Code and Codex CLI ("CLI manages its own
//     agentic loop"), and the bridge MCP config is built to hand to the CLI
//     process directly.
//   - picli_interactive_adapter.go, notably, DOES send these chunk types
//     natively — an inconsistency across coding-agent adapters worth its own
//     fix regardless of what this instrumentation finds.
//
// emitTypedEvent is the one point every event passes through regardless of
// origin — chunk-derived, directly constructed, or something not yet found —
// so a stack trace captured HERE, the first few times a ToolCallStartEvent or
// ToolCallEndEvent carrying a provider-native id is emitted, answers the
// question no further source reading could: which function actually called
// emitTypedEvent with this event.
//
// Deliberately temporary. Delete this file and its one call site in
// emitTypedEvent once the answer is captured from a live run — it is not
// meant to ship as permanent logging.
var (
	debugToolCallOriginMu   sync.Mutex
	debugToolCallOriginLeft = 12 // cap so one busy session cannot flood the log
)

func debugLogToolCallEventOrigin(a *Agent, eventData events.EventData) {
	var toolName, toolCallID string
	switch e := eventData.(type) {
	case *events.ToolCallStartEvent:
		toolName, toolCallID = e.ToolName, e.ToolCallID
	case *events.ToolCallEndEvent:
		toolName, toolCallID = e.ToolName, e.ToolCallID
	default:
		return
	}
	// Only the mystery mechanism: toolcalllog's own ids are always
	// "toolu_<decimal digits>" with no letters after the underscore. A real
	// provider id (Claude's toolu_<mixed-case>, Codex's call_<mixed-case>)
	// contains a letter in that segment.
	if !looksLikeProviderNativeToolCallID(toolCallID) {
		return
	}

	debugToolCallOriginMu.Lock()
	if debugToolCallOriginLeft <= 0 {
		debugToolCallOriginMu.Unlock()
		return
	}
	debugToolCallOriginLeft--
	remaining := debugToolCallOriginLeft
	debugToolCallOriginMu.Unlock()

	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)

	provider := ""
	if a != nil {
		provider = string(a.provider)
	}
	sessionID := ""
	if a != nil {
		sessionID = a.sessionID
	}

	logger := debugToolCallOriginFallbackLogger
	if a != nil && a.logger != nil {
		logger = a.logger
	}
	logger.Warn("[PLAT-149-DEBUG] tool call event with provider-native id reached emitTypedEvent — full stack follows",
		loggerv2.String("provider", provider),
		loggerv2.String("session_id", sessionID),
		loggerv2.String("tool_name", toolName),
		loggerv2.String("tool_call_id", toolCallID),
		loggerv2.Int("remaining_captures", remaining),
		loggerv2.String("stack", string(buf[:n])),
	)
}

// looksLikeProviderNativeToolCallID distinguishes a real provider id from
// toolcalllog's own "toolu_<decimal digits>" counter format.
func looksLikeProviderNativeToolCallID(id string) bool {
	underscore := strings.IndexByte(id, '_')
	if underscore < 0 || underscore == len(id)-1 {
		return false
	}
	suffix := id[underscore+1:]
	if _, err := strconv.ParseUint(suffix, 10, 64); err == nil {
		// Parses cleanly as a plain decimal number — toolcalllog's own shape.
		return false
	}
	return true
}
