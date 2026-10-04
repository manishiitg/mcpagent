package mcpagent

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed claude_native_question_hook.py
var claudeNativeQuestionHook string

// Native questions are useful only when the owning product registered its
// answer lifecycle and a person is attending a retained interactive chat.
// Keeping this separate from Full CLI prevents scheduled runs from waiting.
func (a *Agent) claudeNativeQuestionsEnabled() bool {
	if !a.claudeCodePersistentInteractiveSession || !a.userAnswersNativeQuestions || a.wantsStructuredTransport() || !slices.Contains(a.additionalBridgeTools, "request_clarification") {
		return false
	}
	_, registered := a.lookupDirectTool("request_clarification")
	return registered
}

// BuildClaudeNativeQuestionSettings connects Claude's supported PreToolUse
// answer hook to the same session-scoped clarification bridge as other CLIs.
// The caller must enable AskUserQuestion only for an attended chat with a
// registered request_clarification tool. Existing settings/hooks are retained.
func BuildClaudeNativeQuestionSettings(bridgeConfig, existingSettings string) (string, error) {
	settings := map[string]interface{}{}
	if existingSettings != "" {
		if err := json.Unmarshal([]byte(existingSettings), &settings); err != nil || settings == nil {
			return "", fmt.Errorf("invalid existing Claude hook settings")
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("Claude question hook requires python3: %w", err)
	}
	var bridge struct {
		Servers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(bridgeConfig), &bridge); err != nil {
		return "", fmt.Errorf("invalid Claude question bridge configuration")
	}
	env := bridge.Servers["api-bridge"].Env
	apiURL, err := normalizeBridgeAPIURL(env["MCP_API_URL"])
	if err != nil || env["MCP_API_TOKEN"] == "" || env["MCP_SESSION_ID"] == "" {
		return "", fmt.Errorf("Claude question hook needs a session-scoped bridge")
	}
	config, _ := json.Marshal(map[string]string{"api_url": apiURL, "token": env["MCP_API_TOKEN"], "session_id": env["MCP_SESSION_ID"]})
	script := "import base64, json\nCONFIG = json.loads(base64.b64decode(" + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(config)) + "))\n" + claudeNativeQuestionHook
	dir := filepath.Join(os.TempDir(), "claude-code-hooks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(script))
	path := filepath.Join(dir, "ask-user-question-"+hex.EncodeToString(hash[:16])+".py")
	// Write privately and atomically: concurrent chats cannot overwrite one
	// another's credentials, and the token never appears in a command argument.
	file, err := os.CreateTemp(dir, "question-hook-*.py")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(script); err != nil {
		_ = file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
		settings["hooks"] = hooks
	}
	pre, _ := hooks["PreToolUse"].([]interface{})
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	hooks["PreToolUse"] = append(pre, map[string]interface{}{
		"matcher": "AskUserQuestion",
		"hooks":   []map[string]interface{}{{"type": "command", "command": quote(python) + " " + quote(path), "timeout": 1860}},
	})
	data, err := json.Marshal(settings)
	return string(data), err
}
