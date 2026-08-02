package mcpagent

import (
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// withCLISecurityPolicy attaches an application-resolved launch policy to the
// Agent. The policy is copied immediately and again for each provider call so
// later configuration changes cannot widen an already-running session.
func withCLISecurityPolicy(policy llmtypes.CLISecurityPolicy) agentOption {
	resolved := policy.Clone()
	return func(a *Agent) {
		copyPolicy := resolved.Clone()
		a.cliSecurityPolicy = &copyPolicy
	}
}

func (a *Agent) appendCLISecurityPolicyOption(opts []llmtypes.CallOption, provider llm.Provider) []llmtypes.CallOption {
	if a == nil || a.cliSecurityPolicy == nil {
		return opts
	}
	policy := a.cliSecurityPolicy.Clone()
	// Provider identity comes from the trusted provider selected by AgentWorks,
	// never from a model-authored policy value.
	policy.Provider = strings.ToLower(strings.TrimSpace(string(provider)))
	return append(opts, llm.WithCLISecurityPolicy(policy))
}
