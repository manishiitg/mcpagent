package toolerr

import "testing"

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
