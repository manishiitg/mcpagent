// Package toolerr holds the one definition of "this tool result looks like a
// failure", shared by the in-process agent loop and the HTTP bridge handlers.
//
// It lives outside both because the masked-failure class appears on both paths
// and a second copy would drift silently — the same trap documented for event
// ownership and the retention lists.
package toolerr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Tool-failure logging markers.
//
// Two markers, one prefix. `grep '\[TOOL_ERROR'` returns everything; the
// suffixed forms let you separate what is certain from what is guessed.
//
//	[TOOL_ERROR]         the tool reported failure — err != nil, or IsError set
//	[TOOL_ERROR_SUSPECT] the tool reported SUCCESS but the result reads like a
//	                     failure
//
// The second exists because reported success is not evidence of success. A day
// of rollouts on 2026-08-01 held 34 bridge failures that every surface rendered
// as a green check, because the outer transport returned exit_code 0 and the
// error text was the payload. Content-sniffing catches that class; nothing else
// does.
//
// This deliberately over-matches. A tool whose legitimate output discusses
// errors will be flagged, and that is the accepted trade: a false positive
// costs one log line, a false negative costs an investigation that starts with
// no evidence at all.
const (
	Marker        = "[TOOL_ERROR]"
	SuspectMarker = "[TOOL_ERROR_SUSPECT]"
)

var httpFailurePrefix = regexp.MustCompile(`(?i)^HTTP(?:/\S+)?\s+([45][0-9]{2})(?:\s|$)`)

// CanonicalFailure recognizes only payload signals strong enough to change the
// runtime result from success to error. Suspicious is intentionally broad for
// logging; this function is deliberately narrower because it controls retries,
// terminal state, timing counters, and the IsError value sent back to the LLM.
//
// Bridge results are often JSON nested inside MCP content/text strings, so the
// classifier unwraps those envelopes recursively. Plain prose that merely
// mentions an error remains successful.
func CanonicalFailure(resultText string) (string, bool) {
	return canonicalFailureValue(strings.TrimSpace(resultText), "", 0)
}

// CanonicalFailureForTool suppresses payload promotion for tools whose normal
// job is to return arbitrary problem records or documentation. Their genuine
// executor failures already carry err/IsError; inspecting domain rows such as
// {"status":"failed"} would confuse data with transport state.
func CanonicalFailureForTool(toolName, resultText string) (string, bool) {
	if problemReportingTools[strings.TrimSpace(toolName)] {
		return "", false
	}
	return CanonicalFailure(resultText)
}

// toolNameFromEnvelope matches the one harness-generated error shape observed
// across every unattributed [TOOL_ERROR] marker in the 2026-08-04 scan:
// "... tool execution failed: layer=custom_tool_handler tool=record_pulse_worklist
// session=...". It is deliberately this narrow, not a generic "tool=" scanner,
// because a broader pattern risks matching an unrelated tool name mentioned
// inside a nested envelope and misattributing the marker to it — the transport
// wrapper's own name (what actually failed to relay the call) must win unless
// this exact, unambiguous harness phrase is present.
var toolNameFromEnvelope = regexp.MustCompile(`tool execution failed: layer=\S+ tool=(\S+)`)

// ToolNameFromResult recovers the tool name from a result payload when the
// stream chunk carrying it arrived with an empty name.
//
// This is the last-resort fallback, tried only after the structured
// ToolCallID correlation (matching this end event back to its start event)
// has already failed. On 2026-08-04, 35 of 90 sampled
// "[TOOL_ERROR] CLI tool payload failure" markers carried tool="" — the name
// was only recoverable by regexing the same nested envelope this function
// reads. Making that regex a shared, tested function (instead of leaving each
// caller to write its own ad hoc extraction, or a human to do it by hand while
// reading logs) is the actual fix; the alternative is the marker staying
// attributable in principle but not in practice.
func ToolNameFromResult(resultText string) (string, bool) {
	if m := toolNameFromEnvelope.FindStringSubmatch(resultText); len(m) == 2 {
		name := strings.Trim(m[1], `"'`)
		if name != "" {
			return name, true
		}
	}
	return "", false
}

func canonicalFailureValue(value interface{}, field string, depth int) (string, bool) {
	if depth > 12 || value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return "", false
		}
		if decoded, ok := decodeJSONValue(trimmed); ok {
			if signal, failed := canonicalFailureValue(decoded, field, depth+1); failed {
				return signal, true
			}
		}
		lowered := strings.ToLower(trimmed)
		if strings.HasPrefix(lowered, "error: tool execution failed:") ||
			strings.HasPrefix(lowered, "tool execution failed:") ||
			strings.HasPrefix(lowered, "tool execution canceled:") ||
			strings.HasPrefix(lowered, "tool execution timed out:") {
			return "tool execution failure envelope", true
		}
		if strings.HasPrefix(lowered, "error:") {
			return "error-prefixed result", true
		}
		if match := httpFailurePrefix.FindStringSubmatch(trimmed); len(match) == 2 {
			return "http_status=" + match[1], true
		}
		if field == "stderr" && (strings.Contains(lowered, "permission denied") ||
			strings.Contains(lowered, "operation not permitted") ||
			strings.Contains(lowered, "authorization denied") ||
			strings.Contains(lowered, "access denied")) {
			return "permission denial in stderr", true
		}
	case []interface{}:
		for _, item := range typed {
			if signal, failed := canonicalFailureValue(item, field, depth+1); failed {
				return signal, true
			}
		}
	case map[string]interface{}:
		if explicitBoolean(typed, "success") == boolFalse ||
			explicitBoolean(typed, "ok") == boolFalse {
			return "success=false", true
		}
		if explicitBoolean(typed, "is_error") == boolTrue ||
			explicitBoolean(typed, "iserror") == boolTrue {
			return "is_error=true", true
		}
		if status, ok := stringFieldFold(typed, "status"); ok {
			switch strings.ToLower(strings.TrimSpace(status)) {
			case "error", "failed", "failure", "denied", "unauthorized", "forbidden":
				return "status=" + strings.ToLower(strings.TrimSpace(status)), true
			}
		}
		for _, key := range []string{"exit_code", "exitcode"} {
			if raw, ok := valueFieldFold(typed, key); ok {
				if code, valid := numericCode(raw); valid && code != 0 {
					return fmt.Sprintf("%s=%d", key, code), true
				}
			}
		}
		for _, key := range []string{"status_code", "statuscode", "http_status", "httpstatus"} {
			if raw, ok := valueFieldFold(typed, key); ok {
				if code, valid := numericCode(raw); valid && code >= 400 {
					return fmt.Sprintf("http_status=%d", code), true
				}
			}
		}

		// Recurse through known transport-envelope fields, not arbitrary domain
		// data. A tool may legitimately return a record whose own status is
		// "failed"; inspecting every nested value would turn that discussion into
		// a tool failure.
		for _, key := range []string{"content", "text", "result", "stdout", "stderr", "error"} {
			if nested, ok := valueFieldFold(typed, key); ok {
				if signal, failed := canonicalFailureValue(nested, strings.ToLower(key), depth+1); failed {
					return signal, true
				}
			}
		}
	}
	return "", false
}

func decodeJSONValue(text string) (interface{}, bool) {
	if text == "" || !strings.ContainsAny(text[:1], `{[\"`) {
		return nil, false
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

type boolState uint8

const (
	boolMissing boolState = iota
	boolFalse
	boolTrue
)

func explicitBoolean(values map[string]interface{}, key string) boolState {
	raw, ok := valueFieldFold(values, key)
	if !ok {
		return boolMissing
	}
	value, ok := raw.(bool)
	if !ok {
		return boolMissing
	}
	if value {
		return boolTrue
	}
	return boolFalse
}

func valueFieldFold(values map[string]interface{}, key string) (interface{}, bool) {
	for name, value := range values {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
		if normalized == key || strings.ReplaceAll(normalized, "_", "") == strings.ReplaceAll(key, "_", "") {
			return value, true
		}
	}
	return nil, false
}

func stringFieldFold(values map[string]interface{}, key string) (string, bool) {
	raw, ok := valueFieldFold(values, key)
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	return value, ok
}

func numericCode(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// toolResultErrorSignals are matched case-insensitively against a successful
// tool result. Ordered roughly by how strongly each implies a real failure, and
// the first match is reported so the log says which phrase fired.
var toolResultErrorSignals = []string{
	// The harness envelope itself. This is the highest-value signal in the list:
	// a bridge failure returned as stdout with exit_code 0 is exactly the class
	// that rendered as green checks, and it is unambiguous — no legitimate tool
	// output contains it.
	"tool execution failed:",
	"tool execution canceled:",
	"tool execution timed out:",

	"access denied",
	"permission denied",
	"operation not permitted",
	"unauthorized",
	"forbidden",
	"authentication failed",
	"traceback (most recent call last)",
	"panic:",
	"fatal:",
	"segmentation fault",
	"command not found",
	"no such file or directory",
	"not found",
	"does not exist",
	"connection refused",
	"connection reset",
	"broken pipe",
	"timed out",
	"timeout",
	"context canceled",
	"context deadline exceeded",
	"exception",
	"stacktrace",
	"failed to",
	"failed:",
	"failed.",
	"failed",
	"failure",
	"denied",
	"cannot ",
	"unable to",
	"invalid",
	"refused",
	"error:",
	"error ",
}

// suspiciousToolResult reports whether a result the tool called successful reads
// like a failure, and which signal fired. An empty result is not suspicious:
// plenty of tools legitimately return nothing.
func Suspicious(resultText string) (string, bool) {
	trimmed := strings.TrimSpace(resultText)
	if trimmed == "" {
		return "", false
	}
	lowered := strings.ToLower(trimmed)
	// Strip documented successful-outcome values before scanning for failure
	// signals below, so a value that happens to spell a failure word cannot
	// masquerade as one. See benignJSONOutcomePattern.
	lowered = benignJSONOutcomePattern.ReplaceAllString(lowered, "")

	// A JSON envelope that carries its own status is more reliable than prose,
	// so check those first and report them distinctly.
	for _, explicit := range []string{
		`"success": false`, `"success":false`,
		`"ok": false`, `"ok":false`,
		`"is_error": true`, `"is_error":true`,
		`"iserror": true`, `"iserror":true`,
		`"status": "error"`, `"status":"error"`,
		`"status": "failed"`, `"status":"failed"`,
	} {
		if strings.Contains(lowered, explicit) {
			return explicit, true
		}
	}
	if signal, found := nonZeroExitCodeSignal(lowered); found {
		return signal, true
	}

	for _, signal := range toolResultErrorSignals {
		if strings.Contains(lowered, signal) {
			return strings.TrimSpace(signal), true
		}
	}
	return "", false
}

// benignJSONOutcomePattern matches known JSON field/value pairs where the
// value is a documented, successful outcome of the call rather than a symptom
// of failure -- so the generic signal scan below cannot mistake the value text
// for a failure word.
//
// agent_browser's wait action reports {"waited":"timeout"} when its poll
// deadline elapsed without the awaited condition firing. That is success: the
// tool did what it was told and reported what happened; it is not the same
// signal as a transport or context timeout. Measured 2026-08-17: 142 of 524
// suspects on one workflow (27%) were this exact field, none a real failure.
//
// This must stay narrow. It removes only text this codebase itself produces
// with a known vocabulary, not general prose -- a genuine timeout reported
// anywhere else in the same payload (a different field, a nested error, free
// text) is untouched and still fires normally.
var benignJSONOutcomePattern = regexp.MustCompile(`"waited"\s*:\s*"timeout"`)

// nonZeroExitCodeSignal finds an exit_code field whose value is not 0. Shell
// results carry the real outcome here while the transport reports success, so a
// non-zero value is the single most reliable failure signal available.
func nonZeroExitCodeSignal(lowered string) (string, bool) {
	for _, key := range []string{`"exit_code":`, `"exit_code" :`, `"exitcode":`, "exit_code=", "exit code "} {
		index := strings.Index(lowered, key)
		if index < 0 {
			continue
		}
		rest := strings.TrimSpace(lowered[index+len(key):])
		rest = strings.TrimLeft(rest, `"' `)
		if rest == "" {
			continue
		}
		digits := 0
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		if digits == 0 {
			continue
		}
		value := rest[:digits]
		if strings.Trim(value, "0") != "" {
			return "exit_code=" + value, true
		}
	}
	return "", false
}

// truncateToolResultForLog bounds a tool result so one pathological payload
// cannot flood the log. Matches the 400-byte budget the bridge handlers use.
func TruncateForLog(s string) string {
	return truncateTo(s, 400)
}

func truncateTo(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// problemReportingTools return problem descriptions as their normal payload, so
// content-sniffing them is guaranteed noise. Measured on two live workflows:
// 70 of 173 suspect hits came from these five, and not one was a tool failure —
// a skill doc describing error handling, a Pulse backlog listing "not found"
// findings, a module state quoting concern text.
//
// This suppresses only the heuristic. A genuine failure in these tools still
// returns a non-nil error and is reported under the confirmed Marker, so
// nothing real is lost.
var problemReportingTools = map[string]bool{
	"read_skill":                    true,
	"get_pulse_finding_backlog":     true,
	"get_pulse_module_state":        true,
	"get_pulse_review_result":       true,
	"get_workflow_command_guidance": true,
	"query_workflow_db":             true,
	"record_pulse_worklist":         true,
	"organize_global_learnings":     true,
	"consolidate_knowledgebase":     true,
	// PLAT-127. A message-sequence orchestrator step's route catalog dump
	// (get_route_description with no route_id, listing every configured route's
	// full behavior prose) hit this same shape on 2026-08-17: 12 of 12 suspects
	// on one workflow, zero of them an actual failure -- one whose prose happened
	// to contain a word this heuristic treats as a failure signal.
	"get_route_description": true,
}

// SuspiciousForTool is Suspicious with the per-tool suppression applied. Prefer
// it wherever the tool name is known.
func SuspiciousForTool(toolName, resultText string) (string, bool) {
	if problemReportingTools[strings.TrimSpace(toolName)] {
		return "", false
	}
	return Suspicious(resultText)
}

// Per-field budgets for TruncateArgsForLog. A short diagnostic field must
// survive alongside a long one.
const (
	argFieldBudget = 200
	argTotalBudget = 900
)

// TruncateArgsForLog bounds a tool-argument JSON object field by field instead
// of chopping the serialized blob at a fixed offset.
//
// Head-first truncation loses whichever field happens to serialize late. A real
// case: a diff_patch_workspace_file denial logged only the opening of a large
// `diff`, so `filepath` — the one field that explained the failure — was cut
// off entirely and had to be recovered from the error text. Keys are cheap and
// always diagnostic; only values are trimmed.
//
// Non-object or malformed input falls back to plain truncation, so this is
// never worse than what it replaces.
func TruncateArgsForLog(argsJSON string) string {
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil || len(fields) == 0 {
		return TruncateForLog(trimmed)
	}

	// Sorted so the same call always logs the same way and two lines can be
	// diffed against each other.
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	builder.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(truncateTo(renderArgValue(fields[name]), argFieldBudget))
		if builder.Len() > argTotalBudget {
			builder.WriteString(" ...(more fields omitted)")
			break
		}
	}
	builder.WriteByte('}')
	return builder.String()
}

// renderArgValue prefers the unquoted string form so a path or command reads
// cleanly in a log line, and falls back to the raw JSON for other types.
func renderArgValue(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	return strings.TrimSpace(string(raw))
}
