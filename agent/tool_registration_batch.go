package mcpagent

import "github.com/manishiitg/mcpagent/agent/codeexec"

// DeferToolRegistryPublication batches setup-time RegisterCustomTool calls.
// Call the returned function before serving tools; defer it too for error exits.
// Like RegisterCustomTool, this is for single-owner setup, not concurrent edits.
// Existing canonical definitions and authorization closures are unchanged.
func (a *Agent) DeferToolRegistryPublication() func() {
	a.toolRegistrationDepth++
	finished := false
	return func() {
		if finished {
			return
		}
		finished = true
		a.toolRegistrationDepth--
		a.publishCustomToolRegistry()
	}
}

func (a *Agent) publishCustomToolRegistry() {
	if a.toolRegistrationDepth != 0 || !a.toolRegistryDirty {
		return
	}
	a.toolRegistryDirty = false
	if a.clients == nil {
		return
	}
	executors := a.directToolExecutors()
	codeexec.InitRegistry(a.clients, executors, a.realToolToServer(), a.logger)
	if a.sessionID != "" {
		codeexec.InitRegistryForSession(a.sessionID, executors, a.logger)
	}
}
