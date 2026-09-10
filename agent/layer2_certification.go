package mcpagent

// Layer2Transport is the transport a Layer-2 capability is certified on. Layer 2
// (mcpagent's orchestration) must mask the transport, so the same capability is
// certified separately on each — and the two P0 SETS genuinely differ (see the
// transport model in docs/layer_test_coverage.html): steering on json means
// QUEUE (never live-steer), and multi-turn on json is native --resume rather
// than persistent-pane reuse.
type Layer2Transport string

const (
	Layer2TransportTmux Layer2Transport = "tmux"
	Layer2TransportJSON Layer2Transport = "json"
)

// Layer2Certification is one release-blocking Layer-2 capability: a real-CLI
// e2e test proving mcpagent's own orchestration (bridge, multi-turn/resume,
// steer-vs-queue, tool-failure handling, recording, prompt/skill projection) is
// provider- and transport-agnostic. Distinct from Layer 1
// (multi-llm-provider-go's CodingAgentProviderCertifications), which certifies a
// single adapter call; this certifies the layer above it.
//
// This registry is the machine-checkable source of truth for what
// docs/layer_test_coverage.html describes informally. Before it existed, Layer 2
// had no requiredP0CertificationIDs equivalent — the e2e tests were real but
// nothing enforced them, so coverage could silently regress.
// TestLayer2P0AgentReviewedEvidence closes that gap.
type Layer2Certification struct {
	ID            string          // stable capability id, e.g. "multi_turn.json"
	Transport     Layer2Transport //
	Providers     []string        // providers this capability must be green for (agentreview record suffix)
	TestName      string          // the Go e2e test that proves it
	AgentReviewed bool            // true => an APPROVED agentreview record is the enforceable evidence; false => the test is self-validating (canary / deterministic round-trip)
	Notes         string          //
}

func layer2AllProviders() []string { return []string{"Claude", "Codex", "Cursor", "Pi", "Muse"} }

// layer2TmuxProviders is the persistent-tmux subset: all five coding
// providers, Muse included — mcpagent runs muse on a persistent TUI pane
// (transcript tailing, live-input steering, native --resume) exactly like
// the other four. Kept as a helper (not inline lists) so adding the next
// provider is one line.
func layer2TmuxProviders() []string { return []string{"Claude", "Codex", "Cursor", "Pi", "Muse"} }

// Layer2P0Certifications enumerates the Layer-2 capabilities that must stay
// green. Add a row here when a capability graduates to release-blocking; the
// consistency + evidence tests then enforce it.
var Layer2P0Certifications = []Layer2Certification{
	// --- tmux: agent-reviewed real-CLI evidence ---
	// Muse is certified on every tmux row: it runs on a persistent TUI pane
	// with transcript streaming, mid-turn live-input steering, and native
	// --resume, like the other four. One transport-shaped caveat, enforced by
	// TestTmuxSystemPromptSurvivesNewAgent's Muse branch: muse has no
	// --system-prompt flag, so the system prompt travels via AGENTS.md
	// (project-instruction-only) and the model treats it as untrusted
	// project instructions — benign rules are obeyed, secret-disclosure
	// rules are refused. That is model trust policy, not a transport drop:
	// the file bytes demonstrably reach the model.
	{"multi_turn.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingMultiTurn", true, "persistent-session reuse across turns"},
	{"concurrency.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingConcurrent", true, "parallel sessions stay isolated"},
	{"continuity.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestCodingSessionContinuityAfterLoss", true, "native --resume after session loss"},
	{"steering.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestCodingSessionDeliverSteerMidTurn", true, "mid-turn live-input steering into a running turn"},
	{"tool_failure_recovery.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingToolFailureRecovery", true, "recovers from a mid-stream tool failure"},
	{"tool_failure_giveup.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingToolFailureGiveUp", true, "gives up without fabricating on permanent failure"},
	{"message_modes.tmux", Layer2TransportTmux, []string{"Claude", "Codex", "Cursor", "Muse"}, "TestRealBridgeMessageModes", true, "raw/final/clean-stream reconstruction (Pi excluded: documented model-verbosity non-bug, left strict; Muse: all 3 modes proven, mode1 via change-deduped pane snapshots)"},
	{"markdown_fidelity.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeMarkdownFidelity", true, "GFM table + nested code fence survive extraction byte-exact, on disk and streamed, with Count()==1 duplication guards on structural markers (not presence-only asserts) — added after a user report of duplicate text in pi streaming; live-verified no duplication on any of the 5 providers"},

	// --- tmux: self-validating (canary / deterministic) evidence, no agent review ---
	{"system_prompt.tmux", Layer2TransportTmux, layer2AllProviders(), "TestTmuxSystemPromptSurvivesNewAgent", false, "custom system prompt survives newAgent -> real CLI (57b4dd9 class)"},
	{"skills.tmux", Layer2TransportTmux, layer2AllProviders(), "TestTmuxSkillsSurviveNewAgent", false, "attached skill projected + readable by the model"},
	{"convrecord.tmux", Layer2TransportTmux, layer2AllProviders(), "TestConversationRecordingWritesRealTurnData", false, "record->reload round-trip + real token usage (no USD — pricing is a caller decision)"},

	// --- json/structured: agent-reviewed real-CLI evidence (Claude gained a lane this line of work) ---
	{"multi_turn.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportMultiTurn", true, "native --resume: codex exec resume / cursor --resume / pi --session-id / claude --resume / muse --session-id"},
	{"steering_queue.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportDeliverQueuesMidTurn", true, "query-only transport: Deliver QUEUES, never live-steers"},
	{"tool_failure_recovery.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportToolFailureRecovery", true, "recovers from a mid-stream tool failure"},
	{"tool_failure_giveup.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportToolFailureGiveUp", true, "gives up without fabricating. Codex runs bridge-only with native code-exec features disabled and must emit named execute_shell_command ToolCallStart events, so both execution and UI tool identity are falsifiable"},

	// --- json/structured: self-validating evidence ---
	{"system_prompt.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportSystemPromptSurvivesNewAgent", false, "57b4dd9 regression guard on structured transport"},
	{"skills.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportSkillsSurviveNewAgent", false, "attached skill projected + readable on structured transport"},
	{"convrecord.json", Layer2TransportJSON, layer2AllProviders(), "TestConversationRecordingStructured", false, "record->reload round-trip on structured transport"},
}
