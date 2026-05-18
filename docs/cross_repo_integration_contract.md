# Cross-Repo Integration Contract — mcpagent

This repo is the **middle layer** in the 3-repo LLM pipeline:

```
mcp-agent-builder-go (HTTP API + frontend)
  → mcpagent (orchestrator + agent loop)        ← YOU ARE HERE
    → multi-llm-provider-go (adapter + real CLI)
```

## Canonical Contract

The full integration contract (10 areas, IC-1 through IC-10) is maintained in:

```
multi-llm-provider-go/docs/cross_repo_integration_contract.md
```

## Tests in This Repo

| IC | Area | Test File | Count |
|----|------|-----------|-------|
| IC-1 | Config propagation | `agent/coding_agent_options_test.go` | 6 tests |
| IC-2 | Streaming chunk flow | `agent/llm_generation_streaming_test.go` | 13 tests |
| IC-4 | Session ID & resume | `agent/session_resume_integration_test.go` | 18 subtests |
| IC-5 | Model metadata | `agent/llm_generation_streaming_test.go` | 2 tests |
| IC-6 | Fallback chain | `agent/fallback_parsing_test.go` | 17 subtests |
| IC-7 | Error classification | `agent/error_classification_test.go` | 44 subtests |
| IC-9 | Multi-turn tool context | `agent/cli_tool_history_test.go` | 3 tests |
| IC-10 | MCP bridge config | `agent/coding_agents_bridge_test.go` | 7 tests |

## Key Functions Under Contract

- `extractCodingAgentSessionIDs()` — IC-4: session ID key-per-provider extraction
- `buildStructuredResumeOptions()` — IC-4: resume option injection
- `streamingManager.processChunks()` — IC-2: chunk routing + event emission
- `finishStreaming()` — IC-2: safe close + StreamingEndEvent with metadata
- `classifyLLMError()` — IC-7: error type classification chain
- `parseFallbackModelRef()` — IC-6: cross-provider fallback ref parsing
- `dedupeFallbacks()` — IC-6: fallback deduplication
- `getEffectiveLLMConfig()` — IC-1/IC-6: config promotion + fallback merge
- `BuildBridgeMCPConfig()` — IC-10: MCP bridge JSON config generation
- CLI tool call → JSON → message reconstruction — IC-9: `cli_tool_call_chunks` round-trip
