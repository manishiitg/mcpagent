package toolerr

import (
	"encoding/json"
	"strings"
	"testing"
)

// The payloads below are real, taken from incidents in docs/bugs. Each was
// returned by a tool that reported success, and each was invisible until
// somebody read the transcript by hand.
func TestSuspiciousToolResultCatchesRealMaskedFailures(t *testing.T) {
	cases := []struct {
		name   string
		result string
	}{
		{
			name:   "shell folder-guard denial under exit_code 0",
			result: `{"stdout":"","stderr":"access denied: shell command references absolute host path","exit_code":0}`,
		},
		{
			name:   "non-zero exit code inside a successful envelope",
			result: `{"stdout":"","stderr":"rg: no matches","exit_code":2}`,
		},
		{
			name:   "harness failure envelope returned as stdout",
			result: `tool execution failed: layer=custom_tool_handler tool=diff_patch_workspace_file session=abc`,
		},
		{
			name:   "workspace API cancellation surfaced as content",
			result: `failed to call workspace API: Patch "http://127.0.0.1:18744/...": context canceled`,
		},
		{
			name:   "skill lookup miss",
			result: `attached skill "agent-browser" not found; available skills: builder-reference`,
		},
		{
			name:   "JSON success flag contradicting the transport",
			result: `{"success": false, "error": "tools_unavailable: unknown=[foo]"}`,
		},
		{
			name:   "python traceback in tool output",
			result: "Traceback (most recent call last):\n  File \"main.py\", line 1\nValueError: bad",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signal, suspicious := Suspicious(tc.result)
			if !suspicious {
				t.Fatalf("Suspicious(%q) = false, want true", tc.result)
			}
			if signal == "" {
				t.Fatal("suspicious result reported no signal; the log line would not say why it fired")
			}
		})
	}
}

// Over-matching is the accepted trade, but a few things must stay quiet or the
// marker becomes noise that nobody greps.
func TestSuspiciousToolResultStaysQuietOnOrdinarySuccess(t *testing.T) {
	for _, result := range []string{
		"",
		"   ",
		"ok",
		`{"success": true, "data": {"rows": 3}}`,
		`{"stdout":"hello\n","stderr":"","exit_code":0}`,
		"Wrote 3 files to Workflow/demo/output",
		`{"exit_code": 0}`,
	} {
		if signal, suspicious := Suspicious(result); suspicious {
			t.Errorf("Suspicious(%q) = true (signal %q), want false", result, signal)
		}
	}
}

// exit_code parsing decides severity, so its edges are worth pinning: a leading
// zero is still zero, and a non-numeric value must not be read as a failure.
func TestNonZeroExitCodeSignal(t *testing.T) {
	cases := []struct {
		result string
		want   bool
	}{
		{result: `"exit_code":0`, want: false},
		{result: `"exit_code": 0`, want: false},
		{result: `"exit_code":00`, want: false},
		{result: `"exit_code":1`, want: true},
		{result: `"exit_code": 127`, want: true},
		{result: `"exit_code":"2"`, want: true},
		{result: `"exit_code":null`, want: false},
		{result: `"exit_code":`, want: false},
	}
	for _, tc := range cases {
		_, got := nonZeroExitCodeSignal(tc.result)
		if got != tc.want {
			t.Errorf("nonZeroExitCodeSignal(%q) = %v, want %v", tc.result, got, tc.want)
		}
	}
}

func TestTruncateToolResultForLogBoundsPayload(t *testing.T) {
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	got := TruncateForLog(string(long))
	if len(got) > 400+len("...(truncated)") {
		t.Fatalf("truncated length = %d, want bounded", len(got))
	}
	if short := TruncateForLog("small"); short != "small" {
		t.Fatalf("short result was altered: %q", short)
	}
}

// Suppression must be scoped to the heuristic only. These tools return problem
// text as their normal payload — 70 of 173 live suspect hits came from them and
// none was a real failure — but a genuine error still surfaces under Marker.
func TestSuspiciousForToolSuppressesProblemReportingTools(t *testing.T) {
	problemText := `{"findings":[{"title":"urls.md not found","detail":"permission denied"}]}`

	for _, tool := range []string{
		"read_skill", "get_pulse_finding_backlog", "get_pulse_module_state",
		"get_pulse_review_result", "query_workflow_db",
	} {
		if signal, suspicious := SuspiciousForTool(tool, problemText); suspicious {
			t.Errorf("SuspiciousForTool(%q, ...) = true (signal %q), want suppressed", tool, signal)
		}
	}

	// Everything else keeps the broad behaviour.
	for _, tool := range []string{"execute_shell_command", "agent_browser", "diff_patch_workspace_file", ""} {
		if _, suspicious := SuspiciousForTool(tool, problemText); !suspicious {
			t.Errorf("SuspiciousForTool(%q, ...) = false, want flagged", tool)
		}
	}
}

// The masked failure that motivated the whole detector, captured verbatim from
// a live run: a tool failure re-wrapped as shell stdout under exit_code 0.
func TestSuspiciousForToolCatchesHarnessEnvelopeWrappedInShellSuccess(t *testing.T) {
	live := `{"stdout":"ERROR: tool execution failed: layer=custom_tool_handler tool=get_pulse_review_result session=schedule-cron--46a9b350: sql: no rows in result set","stderr":"","exit_code":0,"execution_time_ms":25}`
	signal, suspicious := SuspiciousForTool("execute_shell_command", live)
	if !suspicious {
		t.Fatal("live masked failure was not flagged")
	}
	if signal != "tool execution failed:" {
		t.Fatalf("signal = %q, want the harness envelope to win over weaker phrases", signal)
	}
}

// The production case: a diff_patch denial whose args were logged head-first,
// so the long `diff` consumed the whole budget and `filepath` — the only field
// that explained the failure — never appeared.
func TestTruncateArgsForLogKeepsShortFieldsBesideLongOnes(t *testing.T) {
	longDiff := "--- /dev/null\n+++ b/x\n" + strings.Repeat("+padding line\n", 400)
	args := map[string]string{
		"diff":     longDiff,
		"filepath": "Workflow/social-media/builder/improve.html",
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}

	got := TruncateArgsForLog(string(encoded))

	if !strings.Contains(got, "Workflow/social-media/builder/improve.html") {
		t.Fatalf("filepath was lost to truncation; got:\n%s", got)
	}
	if !strings.Contains(got, "diff=") {
		t.Fatalf("diff key missing; got:\n%s", got)
	}
	if len(got) > argTotalBudget+128 {
		t.Fatalf("output length %d exceeds the budget", len(got))
	}

	// Head-first truncation is what this replaces: it drops filepath entirely.
	if strings.Contains(TruncateForLog(string(encoded)), "improve.html") {
		t.Fatal("plain truncation kept filepath; the regression this guards would be untestable")
	}
}

func TestTruncateArgsForLogHandlesNonObjectInput(t *testing.T) {
	for _, in := range []string{"", "   ", "not json at all", `["a","b"]`, `{`} {
		got := TruncateArgsForLog(in)
		if len(got) > 400+len("...(truncated)") {
			t.Fatalf("TruncateArgsForLog(%q) = %d bytes, want bounded", in, len(got))
		}
	}
	if got := TruncateArgsForLog(`{"a":1,"b":true,"c":null}`); !strings.Contains(got, "a=1") || !strings.Contains(got, "b=true") {
		t.Fatalf("non-string values not rendered: %s", got)
	}
}
