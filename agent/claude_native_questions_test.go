package mcpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

func TestClaudeNativeQuestionsEmittedOptions(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")
	for _, enforced := range []bool{false, true} {
		for _, mode := range []string{codingAgentToolsMCPOnly, codingAgentToolsFull} {
			for _, scenario := range []string{"attended", "unattended", "nonpersistent", "structured", "unregistered", "unadvertised"} {
				t.Run(fmt.Sprintf("%s/%s/enforced=%v", mode, scenario, enforced), func(t *testing.T) {
					t.Setenv("MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING", fmt.Sprint(enforced))
					a := bridgeTestAgent()
					a.provider = "claude-code"
					a.sessionID = "claude-question-test"
					a.codingAgentToolsMode, a.cliSecurityPolicy = mode, confinedTestPolicy()
					a.userAnswersNativeQuestions = scenario != "unattended"
					a.claudeCodePersistentInteractiveSession = scenario != "nonpersistent"
					if scenario == "structured" {
						a.codingAgentTransport = llm.CodingAgentTransportStructured
					}
					if scenario != "unadvertised" {
						a.additionalBridgeTools = []string{"request_clarification"}
					}
					if scenario != "unregistered" {
						if err := a.registerDirectTool("request_clarification", "Ask for choices", map[string]interface{}{"type": "object"}, func(context.Context, map[string]interface{}) (string, error) { return "", nil }, time.Minute, "human_tools"); err != nil {
							t.Fatal(err)
						}
					}
					opts, err := a.appendClaudeCodeIntegrationOptions(nil, LLMModel{})
					if err != nil {
						t.Fatal(err)
					}
					metadata := metadataFromCallOptions(opts)
					want := scenario == "attended"
					for _, key := range []string{claudecode.MetadataKeyTools, claudecode.MetadataKeyAllowedTools} {
						tools, _ := metadata[key].(string)
						if slices.Contains(strings.Split(tools, ","), "AskUserQuestion") != want {
							t.Fatalf("%s=%s, native question enabled=%v", key, tools, want)
						}
					}
					settings, _ := metadata[claudecode.MetadataKeySettings].(string)
					if strings.Contains(settings, `"matcher":"AskUserQuestion"`) != want {
						t.Fatalf("question hook settings enabled=%v: %s", want, settings)
					}
					if enforced && !strings.Contains(settings, `"matcher":"*"`) {
						t.Fatal("native question hook removed the existing routing policy")
					}
				})
			}
		}
	}
}

func nativeQuestionTestConfig(url, session string) string {
	data, _ := json.Marshal(map[string]interface{}{"mcpServers": map[string]interface{}{"api-bridge": map[string]interface{}{"env": map[string]string{"MCP_API_URL": url, "MCP_API_TOKEN": "question-test-token", "MCP_SESSION_ID": session}}}})
	return string(data)
}

func nativeQuestionTestCommand(t *testing.T, settings string) string {
	t.Helper()
	var config struct {
		Hooks struct {
			Pre []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string
					Timeout int
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &config); err != nil {
		t.Fatal(err)
	}
	for _, item := range config.Hooks.Pre {
		if item.Matcher == "AskUserQuestion" {
			if item.Hooks[0].Timeout <= 1805 || strings.Contains(item.Hooks[0].Command, "question-test-token") {
				t.Fatal("hook timeout or credential handling is invalid")
			}
			return item.Hooks[0].Command
		}
	}
	t.Fatal("native question hook missing")
	return ""
}

func runNativeQuestionHook(t *testing.T, config, payload string) map[string]interface{} {
	t.Helper()
	settings, err := BuildClaudeNativeQuestionSettings(config, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", nativeQuestionTestCommand(t, settings)) // #nosec G204 -- Execute the production hook command generated from trusted test configuration; payload data is sent only on stdin.
	cmd.Stdin = strings.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid hook reply: %s", out.String())
	}
	return result["hookSpecificOutput"].(map[string]interface{})
}

const nativeQuestionTestInput = `{"tool_name":"AskUserQuestion","session_id":"foreign-native-id","tool_use_id":"toolu_one","tool_input":{"questions":[{"header":"Scope","question":"Which scope?","options":[{"label":"Sheets","description":"Keep sheets"},{"label":"Gmail"}],"multiSelect":false},{"question":"Which features?","options":[{"label":"Read"},{"label":"Send"}],"multiSelect":true}],"answers":{"Which scope?":"fabricated answer"}}}`

func TestClaudeNativeQuestionHookMapsSubmittedAnswers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/request_clarification" || r.Header.Get("X-Session-ID") != "owner" || r.Header.Get("Authorization") != "Bearer question-test-token" {
			t.Error("hook used a payload-controlled session or lost authentication")
		}
		var args struct {
			Questions []struct {
				ID, Question string
				Multi        bool `json:"multi_select"`
				Other        bool `json:"allow_other"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args.Questions) != 2 || args.Questions[0].ID != "question-1" || !args.Questions[1].Multi || !args.Questions[1].Other {
			t.Error("native questions were not converted to the UI contract")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": `{"status":"answered","answers":[{"id":"question-1","other_text":"Keep both"},{"id":"question-2","selected_labels":["Read","Send"]}]}`})
	}))
	defer server.Close()
	output := runNativeQuestionHook(t, nativeQuestionTestConfig(server.URL, "owner"), nativeQuestionTestInput)
	if output["permissionDecision"] != "allow" {
		t.Fatalf("submitted choice denied: %v", output)
	}
	updated := output["updatedInput"].(map[string]interface{})
	answers := updated["answers"].(map[string]interface{})
	if answers["Which scope?"] != "Keep both" || answers["Which features?"] != "Read, Send" {
		t.Fatalf("native answer format: %v", answers)
	}
	if len(updated["questions"].([]interface{})) != 2 {
		t.Fatal("original native question input was dropped")
	}
}

func TestClaudeNativeQuestionHookRefusesUnsubmittedOrInvalidAnswers(t *testing.T) {
	for _, response := range []string{
		`{"success":false,"error":"cancelled"}`,
		`{"success":true,"result":{"status":"interrupted","answers":[]}}`,
		`{"success":true,"result":{"status":"answered","answers":[]}}`,
		`{"success":true,"result":{"status":"answered","answers":[{"id":"question-1","selected_labels":["not an option"]},{"id":"question-2","selected_labels":["Read"]}]}}`,
		`{"success":true,"result":{"status":"answered","answers":[{"id":"old-question","selected_labels":["Sheets"]},{"id":"question-2","selected_labels":["Read"]}]}}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
			defer server.Close()
			output := runNativeQuestionHook(t, nativeQuestionTestConfig(server.URL, "owner"), nativeQuestionTestInput)
			if output["permissionDecision"] != "deny" || output["updatedInput"] != nil {
				t.Fatalf("invalid answers were accepted: %v", output)
			}
		})
	}
}

func TestClaudeNativeQuestionHookPrivateAndSessionScoped(t *testing.T) {
	a, err := BuildClaudeNativeQuestionSettings(nativeQuestionTestConfig("http://localhost:8080", "one"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildClaudeNativeQuestionSettings(nativeQuestionTestConfig("http://localhost:8080", "two"), "")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different chats share question hook credentials")
	}
	command := nativeQuestionTestCommand(t, a)
	path := strings.TrimSuffix(strings.Split(command, " ")[1], "'")
	path = strings.TrimPrefix(path, "'")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("hook credentials are not private: %v", err)
	}
	if _, err := BuildClaudeNativeQuestionSettings(`{"mcpServers":{}}`, ""); err == nil {
		t.Fatal("sessionless question hook accepted")
	}
}
