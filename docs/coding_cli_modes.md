# Coding CLI modes: mcp_only and full

## Overview

A coding CLI runs in exactly one of two tool modes. `mcp_only` exposes only
the MCP bridge tools; `full` adds the CLI's own native tools (reads, shell,
edits, subagents) alongside the bridge. Full applies only when the CLI starts
confined (Landlock on Linux, Seatbelt on a Mac) — an unconfined `full` runs as
`mcp_only`, never with native tools. There is no reads-only middle state.

The retired names `hybrid` and `full_unconfined` normalize to `full` (owner
decision 2026-10-03), so old configs keep working under the same confinement
gate.

## Key Files & Locations

| File | Contents |
|---|---|
| `agent/coding_agent_integrations.go` | Mode constants, `fullCLIEnabled()`, per-CLI option builders |
| `agent/agent.go` | `normalizeCodingAgentToolsMode()`, `nativeCodingToolsEnabled()` |
| `agent/coding_agent_modes_contract_test.go` | `TestCodingAgentModesContract`: prompt-vs-launch-options per CLI per mode |

## How the mode is decided

```text
requested mode == "full" AND policy.Confined()  →  full (native tools + bridge)
otherwise                                       →  mcp_only (bridge only)
```

`Confined()` means the Landlock launcher on Linux or Seatbelt on a Mac. Pi has
no confined full mode and stays bridge-only in both modes.

## Per-CLI full behavior

| CLI | Full mode |
|---|---|
| Claude Code | Full native toolset (reads, search, skills, subagents, todos, web, shell, edits) with no permission prompts; subagents inherit the tool list |
| Codex | Shell + subagents, workspace-write |
| Cursor | Native tools via `WithCursorFullNativeTools`; its Delete tool stays denied (delete with the shell); granted folders passed as `--add-dir` |
| Muse | No allowlist (no hook, no `--disable` flags); can list folders above the working folder |
| Agy | Native tools, no hybrid gate; never copies the person's statusLine into the private home |
| Pi | Bridge-only (no full mode yet) |

The routing preamble names each CLI's real tools, the sandbox boundary, and
the protected files. The contract test fails if prompts and launch options
disagree for any CLI in either mode.

## Attended clarification questions

Claude Code's `AskUserQuestion` is enabled in either mode when the host enables
user answers for a retained interactive session, advertises
`request_clarification`, and registers that tool's answer handler. Its
`PreToolUse` hook sends the questions to the host's selectable chat card and
returns only the user's submitted answers. It supports grouped questions,
multi-select, custom text, and subsequent prompts. A cancellation or transport
failure denies the native call rather than choosing an answer.

This question tool does not grant filesystem or shell access. Unattended,
unregistered, nonpersistent, and structured transports keep it disabled. Other
coding CLIs can use the same host-registered `request_clarification` bridge
tool. Hosts must expose the bridge's session-scoped HTTP API and have `python3`
available for the native Claude hook. Existing routing hooks are preserved.

## Mac (Seatbelt) notes

Every coding CLI runs under Seatbelt on a Mac with home open. Practical
consequences: Claude's granted folders arrive as `--add-dir` so its own edits
reach the workflow; Codex pre-trusts its folder in the person's own `~/.codex`
and sweeps stale session profiles; personal Codex MCP stays off; Cursor needs
explicit trust/force for new folders. Unconfined launches (sandbox unavailable)
silently run `mcp_only` — native tools never apply without confinement.

## Retired names

`hybrid` and `full_unconfined` in existing configs read as `full`. Behavior is
unchanged for confined launches; previously unconfined `full_unconfined`
launches now run `mcp_only`.
