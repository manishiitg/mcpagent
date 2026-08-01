package mcpagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// DefinitionAssembly is the pre-run construction surface for legacy factories
// that cannot yet provide AgentDefinition in one expression. It is sealed at
// the immutable-definition boundary and is never available to a Session.
type DefinitionAssembly struct {
	mu     sync.Mutex
	agent  *Agent
	sealed bool
}

func NewDefinitionAssembly(agent *Agent) *DefinitionAssembly {
	return &DefinitionAssembly{agent: agent}
}

func (a *DefinitionAssembly) AddInstructions(instructions ...string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.mutable(); err != nil {
		return err
	}
	a.agent.appendInstructions(instructions...)
	return nil
}

func (a *DefinitionAssembly) ResetInstructions(base string, supplements ...string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.mutable(); err != nil {
		return err
	}
	a.agent.resetInstructions(base, supplements...)
	return nil
}

func (a *DefinitionAssembly) AddSkill(skill *llmtypes.Skill) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.mutable(); err != nil {
		return err
	}
	a.agent.attachSkill(skill)
	return nil
}

func (a *DefinitionAssembly) AddTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.mutable(); err != nil {
		return err
	}
	return a.agent.RegisterCustomToolWithTimeout(name, description, parameters, execute, timeout, displayGroup)
}

func (a *DefinitionAssembly) Snapshot() AgentDefinitionView {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.agent == nil {
		return AgentDefinitionView{}
	}
	return a.agent.Definition()
}

func (a *DefinitionAssembly) Seal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealed = true
}

func (a *DefinitionAssembly) mutable() error {
	if a == nil || a.agent == nil {
		return fmt.Errorf("definition assembly has no agent draft")
	}
	if a.sealed {
		return fmt.Errorf("definition assembly is sealed")
	}
	return nil
}
