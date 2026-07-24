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

func layer2AllProviders() []string { return []string{"Claude", "Codex", "Cursor", "Pi"} }

// Layer2P0Certifications enumerates the Layer-2 capabilities that must stay
// green. Add a row here when a capability graduates to release-blocking; the
// consistency + evidence tests then enforce it.
var Layer2P0Certifications = []Layer2Certification{
	// --- tmux: agent-reviewed real-CLI evidence ---
	{"multi_turn.tmux", Layer2TransportTmux, layer2AllProviders(), "TestRealBridgeStreamingMultiTurn", true, "persistent-session reuse across turns"},
	{"concurrency.tmux", Layer2TransportTmux, layer2AllProviders(), "TestRealBridgeStreamingConcurrent", true, "parallel sessions stay isolated"},
	{"continuity.tmux", Layer2TransportTmux, layer2AllProviders(), "TestCodingSessionContinuityAfterLoss", true, "native --resume after session loss"},
	{"steering.tmux", Layer2TransportTmux, layer2AllProviders(), "TestCodingSessionDeliverSteerMidTurn", true, "mid-turn live-input steering into a running turn"},
	{"tool_failure_recovery.tmux", Layer2TransportTmux, layer2AllProviders(), "TestRealBridgeStreamingToolFailureRecovery", true, "recovers from a mid-stream tool failure"},
	{"tool_failure_giveup.tmux", Layer2TransportTmux, layer2AllProviders(), "TestRealBridgeStreamingToolFailureGiveUp", true, "gives up without fabricating on permanent failure"},
	{"message_modes.tmux", Layer2TransportTmux, []string{"Claude", "Codex", "Cursor"}, "TestRealBridgeMessageModes", true, "raw/final/clean-stream reconstruction (Pi excluded: documented model-verbosity non-bug, left strict)"},

	// --- tmux: self-validating (canary / deterministic) evidence, no agent review ---
	{"system_prompt.tmux", Layer2TransportTmux, layer2AllProviders(), "TestTmuxSystemPromptSurvivesNewAgent", false, "custom system prompt survives NewAgent -> real CLI (57b4dd9 class)"},
	{"skills.tmux", Layer2TransportTmux, layer2AllProviders(), "TestTmuxSkillsSurviveNewAgent", false, "attached skill projected + readable by the model"},
	{"convrecord.tmux", Layer2TransportTmux, layer2AllProviders(), "TestConversationRecordingWritesRealTurnData", false, "record->reload round-trip + real token/cost"},

	// --- json/structured: agent-reviewed real-CLI evidence (Claude gained a lane this line of work) ---
	{"multi_turn.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportMultiTurn", true, "native --resume: codex exec resume / cursor --resume / pi --session-id / claude --resume"},
	{"steering_queue.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportDeliverQueuesMidTurn", true, "query-only transport: Deliver QUEUES, never live-steers"},
	{"tool_failure_recovery.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportToolFailureRecovery", true, "recovers from a mid-stream tool failure"},
	{"tool_failure_giveup.json", Layer2TransportJSON, []string{"Claude", "Cursor", "Pi"}, "TestStructuredTransportToolFailureGiveUp", true, "gives up without fabricating (Codex excluded: unremovable native functions.exec bypasses the bridge, so give-up is unfalsifiable)"},

	// --- json/structured: self-validating evidence ---
	{"system_prompt.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportSystemPromptSurvivesNewAgent", false, "57b4dd9 regression guard on structured transport"},
	{"skills.json", Layer2TransportJSON, layer2AllProviders(), "TestStructuredTransportSkillsSurviveNewAgent", false, "attached skill projected + readable on structured transport"},
	{"convrecord.json", Layer2TransportJSON, layer2AllProviders(), "TestConversationRecordingStructured", false, "record->reload round-trip on structured transport"},
}
