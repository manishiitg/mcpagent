package mcpagent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
)

type codingAgentIntegrationAppender func(*Agent, []llmtypes.CallOption, LLMModel) ([]llmtypes.CallOption, error)

var codingAgentIntegrationAppenders = map[llmproviders.Provider]codingAgentIntegrationAppender{
	llmproviders.ProviderClaudeCode: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendClaudeCodeIntegrationOptions(opts, model)
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
	if a.bridgeReadyFile != "" {
		// Hold the cold session's first prompt until the bridge reports the tools
		// are connected (tools/list answered), so the model never opens with no
		// tools. BuildBridgeMCPConfig set this path just above.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
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

func (a *Agent) appendCodexCLIIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	opts = append(opts, llm.WithCodexDisableShellTool())
	opts = append(opts, llm.WithCodexApprovalPolicy("never"))
	// Bridge-only posture for codex. Unlike claude/cursor/pi, codex ALWAYS
	// advertises a core `functions.exec` tool that cannot be removed by any flag
	// or config (verified: it survives --disable unified_exec/shell_tool/
	// multi_agent/code_mode_*, read-only sandbox, and -c tools.exec=false). So we
	// cannot make codex strictly tool-only-through-the-bridge. Instead we run it
	// READ-ONLY: native exec can read but CANNOT write or mutate the host, so every
	// state change is forced through the MCP bridge (execute_shell_command runs in
	// the executor process, not codex's sandbox, so bridge writes still work).
	// Net guarantee for codex: no native WRITES — all mutations are bridge-routed.
	// See TestRealBridgeStreamingE2E (codex case) which enforces this.
	opts = append(opts, llm.WithCodexSandbox("read-only"))
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
	mcpToolTimeout := codingtimeout.LongRunningMCPToolTimeout()
	apiBridge["tool_timeout_sec"] = int64(mcpToolTimeout / time.Second)
	apiBridge["default_tools_approval_mode"] = "approve"
	mcpServersJSON, err := json.Marshal(mcpServers)
	if err != nil {
		return nil, fmt.Errorf("Codex CLI requires serializable MCP bridge servers: %w", err)
	}
	opts = append(opts, llm.WithCodexMCPServers(string(mcpServersJSON)))
	if a.bridgeReadyFile != "" {
		// Hold a cold codex session's first prompt until the bridge reports the
		// tools connected (tools/list answered) — see BuildBridgeMCPConfig.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	a.Logger.Info(fmt.Sprintf("🌉 [CODEX_CLI] Configured MCP bridge through a session TOML profile (MCP tool timeout=%s, layer=codex_mcp_client)", mcpToolTimeout))

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
