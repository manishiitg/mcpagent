// Package toolerr holds the one definition of "this tool result looks like a
// failure", shared by the in-process agent loop and the HTTP bridge handlers.
//
// It lives outside both because the masked-failure class appears on both paths
// and a second copy would drift silently — the same trap documented for event
// ownership and the retention lists.
package toolerr

import (
	"encoding/json"
	"sort"
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
