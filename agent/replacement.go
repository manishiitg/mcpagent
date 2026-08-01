package mcpagent

// RetireReplacedAgent stops per-instance background work after an immutable
// definition replacement. It deliberately preserves shared MCP connections,
// tracers, provider continuation state, and projected workspace artifacts now
// owned by the replacement instance.
func RetireReplacedAgent(agent *Agent) {
	if agent == nil {
		return
	}
	agent.stopCleanupRoutine()
}
