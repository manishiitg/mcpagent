package mcpagent

import (
	"encoding/json"
	"fmt"
	"strings"
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
	llmproviders.ProviderPiCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendPiCLIIntegrationOptionsForModel(opts, model)
	},
}

const (
	codingAgentToolsMCPOnly  = "mcp_only"
	codingAgentToolsHybrid   = "hybrid"
	codingAgentApprovalsAuto = "provider_auto"
	codingAgentApprovalsAll  = "approve_all"
)

func (a *Agent) nativeCodingToolsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(a.codingAgentToolsMode)) {
	case codingAgentToolsHybrid:
		return true
	default:
		return false
	}
}

func (a *Agent) approveAllCodingTools() bool {
	return strings.EqualFold(strings.TrimSpace(a.codingAgentApprovalsMode), codingAgentApprovalsAll)
}

func (a *Agent) appendClaudeCodeIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	claudeHTTPHooksEnabled := claudeHTTPRoutingHooksEnabled()

	// Use restricted permissions instead of skipping them entirely. Allow our
	// bridge tools and WebSearch to run without prompts. In enforced mode the
	// wildcard is replaced with an explicit allowlist DERIVED from the actual
	// registered bridge tool set (core + withAdditionalBridgeTools) — not a
	// hardcoded 4-tool literal, which silently rejected any additional tool a
	// caller had registered.
	allowedTools := "mcp__api-bridge__*,WebSearch"
	if claudeHTTPHooksEnabled {
		allowedTools = strings.Join(claudeBridgeAllowedToolIdentifiers(a.additionalBridgeTools, a.admitsBridgeTool), ",") + ",WebSearch"
	}
	opts = append(opts, llm.WithAllowedTools(allowedTools))

	if a.nativeCodingToolsEnabled() {
		// "default" re-enables Claude Code's normal filesystem/shell/browser
		// tools. The MCP bridge remains mounted for product tools.
		opts = append(opts, llm.WithClaudeCodeTools("default"))
		if a.approveAllCodingTools() {
			opts = append(opts, llmproviders.WithDangerouslySkipPermissions())
		} else {
			opts = append(opts, llm.WithClaudeCodePermissionMode("auto"))
		}
	} else {
		// Force Claude to use our custom tools by disabling its own internal ones.
		opts = append(opts, llm.WithClaudeCodeTools("WebSearch"))
	}

	if claudeHTTPHooksEnabled {
		hookPath, hookErr := writeClaudeHTTPRoutingHook(a.additionalBridgeTools, a.admitsBridgeTool)
		if hookErr != nil {
			a.logger.Warn("Failed to write Claude Code HTTP routing hook", loggerv2.Error(hookErr))
		} else {
			settingsJSON, settingsErr := buildClaudeHTTPRoutingSettings(hookPath)
			if settingsErr != nil {
				a.logger.Warn("Failed to build Claude Code hook settings", loggerv2.Error(settingsErr))
			} else {
				opts = append(opts, llm.WithClaudeCodeSettings(settingsJSON))
				a.logger.Info("🪝 Claude Code HTTP tool routing enforcement enabled",
					loggerv2.String("env", "MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING"),
					loggerv2.String("hook_path", hookPath))
			}
		}
	}

	bridgeConfig, err := a.buildBridgeMCPConfig()
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
	a.logger.Info("🌉 Using MCP bridge for Claude Code tool access via HTTP API")

	if a.maxTurns > 0 {
		opts = append(opts, llm.WithMaxTurns(a.maxTurns))
	}
	if a.claudeCodeSessionID != "" {
		opts = append(opts, llm.WithResumeSessionID(a.claudeCodeSessionID))
	}
	if a.wantsStructuredTransport() {
		opts = append(opts, llm.WithClaudeStructuredTransport(true))
	} else if a.enableStreaming {
		opts = append(opts, llmproviders.WithClaudeStreamTranscript(true))
		// Transcript events power formatted chat, while terminal snapshots power
		// raw/terminal consumers. They are independent projections of the same
		// retained session and must remain available together.
		opts = append(opts, llm.WithClaudeStreamTmuxScreen(true))
	}
	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
			opts = append(opts, llm.WithClaudeCodeEffort(effort))
			a.logger.Info(fmt.Sprintf("🧠 [CLAUDE_CODE] Effort level set to: %s", effort))
		}
	}
	// Mid-turn assistant TEXT only streams when the adapter is explicitly told
	// to tail its transcript for content (separate from EnableStreaming, which
	// is auto-enabled above purely for tool-call observability and does NOT
	// imply this). Only worth the tailing cost when a caller actually
	// registered a StreamingCallback to consume the content.
	if a.streamingCallback != nil {
		opts = append(opts, llm.WithClaudeStreamTranscript(true))
		opts = append(opts, llm.WithClaudeStreamTmuxScreen(true))
	}
	return opts, nil
}

func (a *Agent) appendCodexCLIIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	if !a.nativeCodingToolsEnabled() {
		opts = append(opts, llm.WithCodexDisableShellTool())
		opts = append(opts, llm.WithCodexApprovalPolicy("never"))
	} else if a.approveAllCodingTools() {
		opts = append(opts, llm.WithCodexApprovalPolicy("never"))
	} else {
		// Current Codex supports a stable approval reviewer. Keep the standard
		// workspace sandbox; do not use dangerous bypass, which would remove it.
		opts = append(opts, llm.WithCodexApprovalPolicy("untrusted"))
	}
	// Shell/exec containment: WithCodexDisableShellTool above turns OFF codex's
	// built-in shell_tool + the other native code-exec features (unified_exec,
	// tool_search, browser/computer use, …) via codex's first-class `--disable`
	// flags. Verified live against codex v0.145.0: with those disabled and only
	// the MCP bridge in the session, codex has NO native way to run a command and
	// is forced through the bridge (it reports it has no shell rather than
	// shelling out; TestStructuredTransportToolFailure{Recovery,GiveUp}/Codex now
	// route through the bridge with calls>=2). This CORRECTS a long-standing
	// belief — previously baked into this comment and the transport docs — that
	// codex's native `functions.exec` was "unremovable by any flag". That was
	// true of an older codex; it is not true of the current one. For SHELL, codex
	// is now containable to the bridge exactly like claude/cursor/pi.
	//
	// IMPORTANT caveat: containment only holds while the session exposes no OTHER
	// code-exec tool. Codex will use any available one (e.g. a node_repl-style MCP
	// that can child_process out) as an escape hatch, so the bridge-only guarantee
	// is "shell_tool disabled AND the MCP set is the bridge alone".
	//
	// Native WRITES are a separate axis: disabling shell_tool does not necessarily
	// disable every mutating native tool codex may expose (e.g. apply_patch), so
	// the sandbox mode below still governs whether codex can mutate the host
	// directly vs. having to route writes through the bridge.
	//
	// DEFAULT is WORKSPACE-WRITE (native writes + no network unless requested):
	// this matches how codex ran for most of this project's life and is right
	// for the common case — an interactive session, or one where the bridge
	// already grants shell access anyway (native write containment buys nothing
	// there; the bridge can already write). Only a session that deliberately
	// restricts its tool set (e.g. "web_search only, no shell on the bridge") or
	// needs every action to hit an audit trail that native exec would bypass
	// needs the stronger guarantee — that caller opts INTO "read-only" via
	// Agent.CodexSandboxMode / withCodexSandbox. Under read-only, native exec can
	// read but CANNOT write or mutate the host, so every state change is forced
	// through the MCP bridge (execute_shell_command runs in the executor
	// process, not codex's sandbox, so bridge writes still work) — but note
	// there is no read-only+network mode (network is unconditionally off), and
	// codex tends to disengage from tools entirely when its own preamble says
	// "read-only, no network", so read-only is a deliberate, narrow opt-in, not
	// something to reach for casually. See TestRealBridgeStreamingE2E (codex
	// case), which explicitly opts into read-only to keep that guarantee tested.
	sandboxMode := a.codexSandboxMode
	if strings.TrimSpace(sandboxMode) == "" {
		sandboxMode = "workspace-write"
	}
	opts = append(opts, llm.WithCodexSandbox(sandboxMode))
	configOverrides := make([]string, 0, 2)
	if sandboxMode == "workspace-write" && a.codexNetworkAccess {
		configOverrides = append(configOverrides, "sandbox_workspace_write.network_access=true")
	}
	if a.nativeCodingToolsEnabled() && !a.approveAllCodingTools() {
		configOverrides = append(configOverrides, `approvals_reviewer="auto_review"`)
	}
	if len(configOverrides) > 0 {
		opts = append(opts, llm.WithCodexConfigOverrides(configOverrides))
	}
	if a.codexSessionID != "" {
		opts = append(opts, llm.WithCodexResumeSessionID(a.codexSessionID))
	}

	bridgeConfig, bridgeErr := a.buildBridgeMCPConfig()
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
	a.logger.Info(fmt.Sprintf("🌉 [CODEX_CLI] Configured MCP bridge through a session TOML profile (MCP tool timeout=%s, layer=codex_mcp_client)", mcpToolTimeout))

	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
			opts = append(opts, llm.WithCodexReasoningEffort(effort))
			a.logger.Info(fmt.Sprintf("🧠 [CODEX_CLI] Reasoning effort set to: %s", effort))
		}
	}
	a.logger.Info("🌉 Using Codex CLI with shell disabled, MCP bridge, and auto-approval")
	if a.wantsStructuredTransport() {
		opts = append(opts, llm.WithCodexStructuredTransport(true))
	} else if a.enableStreaming {
		opts = append(opts, llmproviders.WithCodexStreamTranscript(true))
		opts = append(opts, llm.WithCodexStreamTmuxScreen(true))
	}
	// See appendClaudeCodeIntegrationOptions' matching comment: content
	// streaming needs this separate, explicit opt-in beyond EnableStreaming.
	if a.streamingCallback != nil {
		opts = append(opts, llm.WithCodexStreamTranscript(true))
		opts = append(opts, llm.WithCodexStreamTmuxScreen(true))
	}
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
