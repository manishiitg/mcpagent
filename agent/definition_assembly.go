package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	if agent == nil {
		return &DefinitionAssembly{}
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.definitionAssembly == nil {
		agent.definitionAssembly = &DefinitionAssembly{agent: agent}
	}
	return agent.definitionAssembly
}

func AddDefinitionInstructions(agent *Agent, instructions ...string) error {
	return NewDefinitionAssembly(agent).AddInstructions(instructions...)
}

func ResetDefinitionInstructions(agent *Agent, base string, supplements ...string) error {
	return NewDefinitionAssembly(agent).ResetInstructions(base, supplements...)
}

func AddDefinitionSkill(agent *Agent, skill *llmtypes.Skill) error {
	return NewDefinitionAssembly(agent).AddSkill(skill)
}

func AddDefinitionTool(agent *Agent, name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), displayGroup string) error {
	return NewDefinitionAssembly(agent).AddTool(name, description, parameters, execute, 0, displayGroup)
}

func AddDefinitionToolWithTimeout(agent *Agent, name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	return NewDefinitionAssembly(agent).AddTool(name, description, parameters, execute, timeout, displayGroup)
}

func AddDefinitionObserver(agent *Agent, observer AgentEventListener) error {
	return NewDefinitionAssembly(agent).AddObserver(observer)
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
	return a.agent.registerCustomToolWithTimeout(name, description, parameters, execute, timeout, displayGroup)
}

func (a *DefinitionAssembly) AddObserver(observer AgentEventListener) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.mutable(); err != nil {
		return err
	}
	if observer == nil {
		return fmt.Errorf("definition observer cannot be nil")
	}
	a.agent.addEventListener(observer)
	return nil
}

// Build seals the draft identity and constructs the immutable runtime Agent.
// The caller must swap the returned Agent into its owner before retiring the
// draft. A failed build leaves the assembly mutable so callers can correct the
// definition and retry.
func (a *DefinitionAssembly) Build(ctx context.Context, runtime RuntimeConfig) (*Agent, AgentDefinition, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.mutable(); err != nil {
		return nil, AgentDefinition{}, err
	}

	view := a.agent.Definition()
	direct, err := directToolDefinitions(a.agent.customTools)
	if err != nil {
		return nil, AgentDefinition{}, err
	}
	definition := AgentDefinition{
		Instructions: view.Instructions,
		Skills:       view.SkillDefinitions,
		Tools: ToolSet{
			Direct: direct,
		},
	}
	if a.agent.definition != nil {
		definition.Tools.MCP = append([]MCPToolSource(nil), a.agent.definition.Tools.MCP...)
	}

	next, err := NewAgentFromDefinition(ctx, definition, runtime)
	if err != nil {
		return nil, AgentDefinition{}, err
	}
	for _, observer := range view.Observers {
		if observer != nil {
			next.addEventListener(observer)
		}
	}
	next.PromptLogLabel = a.agent.PromptLogLabel
	next.APIKeys = a.agent.APIKeys
	next.CodingAgentWorkingDir = a.agent.CodingAgentWorkingDir
	next.definitionAssembly = &DefinitionAssembly{agent: next, sealed: true}
	a.sealed = true
	return next, definition, nil
}

func directToolDefinitions(customTools map[string]CustomTool) ([]ToolDefinition, error) {
	names := make([]string, 0, len(customTools))
	for name := range customTools {
		names = append(names, name)
	}
	sort.Strings(names)

	direct := make([]ToolDefinition, 0, len(names))
	for _, name := range names {
		tool := customTools[name]
		if tool.Definition.Function == nil || tool.Execution == nil {
			continue
		}
		var schema map[string]interface{}
		if tool.Definition.Function.Parameters != nil {
			encoded, err := json.Marshal(tool.Definition.Function.Parameters)
			if err != nil {
				return nil, fmt.Errorf("encode tool schema %q: %w", name, err)
			}
			if err := json.Unmarshal(encoded, &schema); err != nil {
				return nil, fmt.Errorf("decode tool schema %q: %w", name, err)
			}
		}
		direct = append(direct, ToolDefinition{
			Name:         name,
			Description:  tool.Definition.Function.Description,
			InputSchema:  schema,
			Execute:      tool.Execution,
			Timeout:      tool.Timeout,
			DisplayGroup: tool.Category,
		})
	}
	return direct, nil
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
