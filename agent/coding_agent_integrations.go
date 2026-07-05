package mcpagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type codingAgentIntegrationAppender func(*Agent, []llmtypes.CallOption, LLMModel) ([]llmtypes.CallOption, error)

var codingAgentIntegrationAppenders = map[llmproviders.Provider]codingAgentIntegrationAppender{
	llmproviders.ProviderClaudeCode: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendClaudeCodeIntegrationOptions(opts, model)
	},
	llmproviders.ProviderGeminiCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendGeminiCLIIntegrationOptions(opts)
	},
	llmproviders.ProviderCodexCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendCodexCLIIntegrationOptions(opts, model)
	},
	llmproviders.ProviderCursorCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendCursorCLIIntegrationOptions(opts)
	},
	llmproviders.ProviderAgyCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendAgyCLIIntegrationOptions(opts)
	},
	llmproviders.ProviderPiCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendPiCLIIntegrationOptionsForModel(opts, model)
	},
}

func (a *Agent) appendClaudeCodeIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	claudeHTTPHooksEnabled := claudeHTTPRoutingHooksEnabled()

	// Use restricted permissions instead of skipping them entirely. Allow our
	// bridge tools and WebSearch to run without prompts.
	allowedTools := "mcp__api-bridge__*,WebSearch"
	if claudeHTTPHooksEnabled {
		allowedTools = "mcp__api-bridge__execute_shell_command,mcp__api-bridge__diff_patch_workspace_file,mcp__api-bridge__agent_browser,mcp__api-bridge__get_api_spec,WebSearch"
	}
	opts = append(opts, llm.WithAllowedTools(allowedTools))

	// Force Claude to use our custom tools by disabling its own internal ones.
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

	if a.MaxTurns > 0 {
		opts = append(opts, llm.WithMaxTurns(a.MaxTurns))
	}
	if a.ClaudeCodeSessionID != "" {
		opts = append(opts, llm.WithResumeSessionID(a.ClaudeCodeSessionID))
	}
	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
			opts = append(opts, llm.WithClaudeCodeEffort(effort))
			a.Logger.Info(fmt.Sprintf("🧠 [CLAUDE_CODE] Effort level set to: %s", effort))
		}
	}
	return opts, nil
}

func (a *Agent) appendGeminiCLIIntegrationOptions(opts []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	a.ensureGeminiProjectDirID()
	var projectDir string
	if !a.IsolatedSessionWorkspace && strings.TrimSpace(a.CodingAgentWorkingDir) != "" {
		projectDir = filepath.Join(a.CodingAgentWorkingDir, ".gemini-main")
	} else {
		projectDir = filepath.Join(os.TempDir(), "gemini-cli-project-"+a.GeminiProjectDirID)
	}

	settings := map[string]interface{}{
		"ui": map[string]interface{}{
			"hideBanner":        true,
			"hideTips":          true,
			"showShortcutsHint": false,
			"footer": map[string]interface{}{
				"hideSandboxStatus": true,
			},
		},
	}
	debugHooksEnabled := geminiDebugHooksEnabled()
	httpRoutingHooksEnabled := geminiHTTPRoutingHooksEnabled()
	if debugHooksEnabled {
		settings["hooks"] = buildGeminiDebugHooks()
		a.Logger.Info("🪝 Gemini CLI BeforeTool debug hook enabled",
			loggerv2.String("env", "MCPAGENT_GEMINI_DEBUG_HOOKS"),
			loggerv2.String("project_dir", projectDir))
	}
	if httpRoutingHooksEnabled {
		a.Logger.Info("🔒 Gemini CLI HTTP tool routing policy enabled",
			loggerv2.String("env", "MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING"),
			loggerv2.String("project_dir", projectDir))
	}

	bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Gemini CLI requires the MCP bridge: %w", bridgeErr)
	}
	var bridgeParsed map[string]interface{}
	if err := json.Unmarshal([]byte(bridgeConfig), &bridgeParsed); err != nil {
		return nil, fmt.Errorf("Gemini CLI requires valid MCP bridge config: %w", err)
	}
	mcpServers, ok := bridgeParsed["mcpServers"]
	if !ok {
		return nil, fmt.Errorf("Gemini CLI requires MCP bridge config with mcpServers")
	}
	settings["mcpServers"] = mcpServers

	settingsBytes, _ := json.Marshal(settings)
	opts = append(opts, llm.WithGeminiProjectSettings(string(settingsBytes)))

	policiesDir := filepath.Join(projectDir, ".gemini", "policies")
	if err := os.MkdirAll(policiesDir, 0750); err != nil {
		a.Logger.Warn("Failed to create Gemini CLI policies directory", loggerv2.Error(err))
	} else {
		policyPath := filepath.Join(policiesDir, "restrict-tools.toml")
		if err := os.WriteFile(policyPath, []byte(geminiRestrictToolsPolicyContent()), 0600); err != nil {
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
	if a.GeminiSessionID != "" {
		opts = append(opts, llm.WithGeminiResumeSessionID(a.GeminiSessionID))
	}
	opts = append(opts, llm.WithGeminiProjectDirID(a.GeminiProjectDirID))
	if !a.IsolatedSessionWorkspace && strings.TrimSpace(a.CodingAgentWorkingDir) != "" {
		opts = append(opts, llm.WithGeminiProjectDirAbsolute(projectDir))
	}
	if strings.TrimSpace(a.CodingAgentWorkingDir) != "" {
		opts = append(opts, llm.WithGeminiWorkingDir(a.CodingAgentWorkingDir))
		a.Logger.Info(fmt.Sprintf("[GEMINI_CLI] Using working dir: %s, project dir: %s, project dir ID: %s (session: %s)", a.CodingAgentWorkingDir, projectDir, a.GeminiProjectDirID, a.GeminiSessionID))
	} else {
		a.Logger.Info(fmt.Sprintf("[GEMINI_CLI] Using project dir ID: %s (session: %s)", a.GeminiProjectDirID, a.GeminiSessionID))
	}
	return opts, nil
}

func (a *Agent) appendCodexCLIIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	opts = append(opts, llm.WithCodexDisableShellTool())
	opts = append(opts, llm.WithCodexApprovalPolicy("never"))
	opts = append(opts, llm.WithCodexSandbox("workspace-write"))
	if a.CodexSessionID != "" {
		opts = append(opts, llm.WithCodexResumeSessionID(a.CodexSessionID))
	}

	bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Codex CLI requires the MCP bridge: %w", bridgeErr)
	}
	var bridgeParsed map[string]interface{}
	if err := json.Unmarshal([]byte(bridgeConfig), &bridgeParsed); err != nil {
		return nil, fmt.Errorf("Codex CLI requires valid MCP bridge config: %w", err)
	}
	mcpServers, ok := bridgeParsed["mcpServers"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Codex CLI requires MCP bridge config with mcpServers")
	}
	apiBridge, ok := mcpServers["api-bridge"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Codex CLI requires MCP bridge config with api-bridge server")
	}
	var configOverrides []string
	if cmd, ok := apiBridge["command"].(string); ok {
		configOverrides = append(configOverrides, fmt.Sprintf("mcp_servers.api-bridge.command=%q", cmd))
	}
	if envMap, ok := apiBridge["env"].(map[string]interface{}); ok {
		for k, v := range envMap {
			if vStr, ok := v.(string); ok {
				configOverrides = append(configOverrides, fmt.Sprintf("mcp_servers.api-bridge.env.%s=%q", k, vStr))
			}
		}
	}
	configOverrides = append(configOverrides, "mcp_servers.api-bridge.tool_timeout_sec=5400")
	opts = append(opts, llm.WithCodexConfigOverrides(configOverrides))
	a.Logger.Info(fmt.Sprintf("🌉 [CODEX_CLI] Configured MCP bridge with %d config overrides", len(configOverrides)))

	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
			opts = append(opts, llm.WithCodexReasoningEffort(effort))
			a.Logger.Info(fmt.Sprintf("🧠 [CODEX_CLI] Reasoning effort set to: %s", effort))
		}
	}
	a.Logger.Info("🌉 Using Codex CLI with shell disabled, MCP bridge, and auto-approval")
	return opts, nil
}

func (a *Agent) appendPiCLIIntegrationOptionsForModel(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	var err error
	opts, err = a.appendPiCLIIntegrationOptions(opts)
	if err != nil {
		return nil, err
	}
	if model.Options != nil {
		if provider, ok := model.Options["pi_provider"].(string); ok && provider != "" {
			opts = append(opts, llm.WithPiProvider(provider))
		}
	}
	return opts, nil
}
