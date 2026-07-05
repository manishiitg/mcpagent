package mcpagent

import "github.com/manishiitg/mcpagent/llm"

func (a *Agent) shouldUseWrapperTokenCounting() bool {
	if a == nil || a.toolOutputHandler == nil {
		return false
	}
	return !llm.IsCodingAgentProvider(a.provider, a.ModelID)
}
