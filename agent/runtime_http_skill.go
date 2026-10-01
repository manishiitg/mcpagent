package mcpagent

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

const runtimeHTTPSkillName = "runtime-http-tools"

// One tutorial body serves legacy inline prompts and progressive skill loading.
const runtimeHTTPInstructions = "Call HTTP tools through execute_shell_command: curl --fail-with-body -sS --json '<payload>' -H \"$MCP_AUTH\" \"$MCP_CUSTOM/<tool>\" (real MCP: $MCP_MCP/<server>/<tool>). Custom groups are labels, not URL segments. MCP_AUTH is already the complete `Authorization: Bearer ...` header. --json already selects POST and Content-Type. Keep curl unpiped to preserve its nonzero HTTP-failure status. Encode complex payloads with a JSON encoder rather than hand-quoting. Check the response envelope's success/error before using result. The bridge environment is configured; report actual tool failures.\n"

// Runtime transport instructions are mcpagent-owned. This skill teaches HTTP
// mechanics without listing tools or granting authority; current schemas win.
func (a *Agent) ensureRuntimeHTTPSkill() error {
	if !a.useCodeExecutionMode || !a.toolDiscovery {
		return nil
	}
	return a.attachSkill(&llmtypes.Skill{
		Name:        runtimeHTTPSkillName,
		Description: "Read before executing platform or connected-app tools over HTTP: discovery, exact schemas/routes, auth headers, JSON quoting and response/error handling. Grants no tool or filesystem permission.",
		Content:     "# HTTP tool execution\n\nDiscover exact admitted names through search_tools; broaden or enumerate a returned group/server when a query misses. Load get_api_spec for the selected names and use its current schema and route. Never guess a name or treat a prior discovery result as authority. Native declared tools use their provider's exact identifiers; read_skill is intrinsic, never HTTP.\n\n" + runtimeHTTPInstructions,
		Source:      llmtypes.SkillSource{Origin: "builtin"},
	})
}

func (a *Agent) hasRuntimeHTTPSkill() bool {
	for _, skill := range a.attachedSkills {
		if skill != nil && skill.Name == runtimeHTTPSkillName {
			return true
		}
	}
	return false
}
