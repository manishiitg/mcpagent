package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type toolImplementationKind string

const (
	toolImplementationMCP     toolImplementationKind = "mcp"
	toolImplementationDirect  toolImplementationKind = "direct"
	toolImplementationVirtual toolImplementationKind = "virtual"
)

// registeredTool is the single internal record used for discovery, schema
// resolution, authorization, and execution routing. Source and DisplayGroup
// are internal metadata; Name is the only model-facing address.
type registeredTool struct {
	Name         string
	Definition   llmtypes.Tool
	Kind         toolImplementationKind
	Source       string
	DisplayGroup string
	Executor     ToolExecutor
	Timeout      time.Duration
}

type canonicalToolRegistry struct {
	mu     sync.RWMutex
	byName map[string]registeredTool
}

func newCanonicalToolRegistry() *canonicalToolRegistry {
	return &canonicalToolRegistry{byName: make(map[string]registeredTool)}
}

func (r *canonicalToolRegistry) register(tool registeredTool) error {
	if tool.Name == "" {
		return fmt.Errorf("cannot register tool with empty name")
	}
	if tool.Definition.Function == nil {
		return fmt.Errorf("tool %q has no function definition", tool.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, exists := r.byName[tool.Name]; exists {
		if existing.Kind != tool.Kind || existing.Source != tool.Source {
			return fmt.Errorf(
				"tool name %q is owned by %s %q and cannot also be owned by %s %q",
				tool.Name,
				existing.Kind,
				existing.Source,
				tool.Kind,
				tool.Source,
			)
		}
		if tool.Kind == toolImplementationDirect && existing.DisplayGroup != tool.DisplayGroup {
			return fmt.Errorf(
				"tool name %q is already registered in category %q and cannot be registered in category %q",
				tool.Name,
				existing.DisplayGroup,
				tool.DisplayGroup,
			)
		}
	}
	r.byName[tool.Name] = tool
	return nil
}

func (r *canonicalToolRegistry) lookup(name string) (registeredTool, bool) {
	if r == nil {
		return registeredTool{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.byName[name]
	return tool, ok
}

func (r *canonicalToolRegistry) snapshot() []registeredTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]registeredTool, 0, len(r.byName))
	for _, tool := range r.byName {
		result = append(result, tool)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *canonicalToolRegistry) directSnapshot() []registeredTool {
	all := r.snapshot()
	direct := make([]registeredTool, 0, len(all))
	for _, tool := range all {
		if tool.Kind == toolImplementationDirect {
			direct = append(direct, tool)
		}
	}
	return direct
}

func (a *Agent) initializeCanonicalToolRegistry(mcpTools []llmtypes.Tool, toolToServer map[string]string) error {
	registry := newCanonicalToolRegistry()
	for _, definition := range mcpTools {
		if definition.Function == nil {
			continue
		}
		name := definition.Function.Name
		server := toolToServer[name]
		if server == "" {
			return fmt.Errorf("MCP tool %q has no owning server", name)
		}
		if err := registry.register(registeredTool{
			Name:       name,
			Definition: definition,
			Kind:       toolImplementationMCP,
			Source:     server,
		}); err != nil {
			return err
		}
	}
	a.canonicalRegistryMu.Lock()
	a.toolRegistry = registry
	a.canonicalRegistryMu.Unlock()
	return nil
}

func (a *Agent) canonicalRegistry() (*canonicalToolRegistry, error) {
	a.canonicalRegistryMu.Lock()
	defer a.canonicalRegistryMu.Unlock()
	if a.toolRegistry != nil {
		return a.toolRegistry, nil
	}

	registry := newCanonicalToolRegistry()
	definitions := append([]llmtypes.Tool(nil), a.allMCPToolDefs...)
	definitions = append(definitions, a.tools...)
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.Function == nil {
			continue
		}
		name := definition.Function.Name
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		server := a.toolToServer[name]
		if server == "" || server == "custom" {
			continue
		}
		if err := registry.register(registeredTool{Name: name, Definition: definition, Kind: toolImplementationMCP, Source: server}); err != nil {
			return nil, err
		}
	}
	a.toolRegistry = registry
	return registry, nil
}

func (a *Agent) lookupDirectTool(name string) (registeredTool, bool) {
	registry, err := a.canonicalRegistry()
	if err != nil {
		return registeredTool{}, false
	}
	tool, ok := registry.lookup(name)
	if !ok || tool.Kind != toolImplementationDirect {
		return registeredTool{}, false
	}
	return tool, true
}

func (a *Agent) directToolSnapshot() []registeredTool {
	registry, err := a.canonicalRegistry()
	if err != nil {
		return nil
	}
	return registry.directSnapshot()
}

func (a *Agent) directToolExecutors() map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	for _, tool := range a.directToolSnapshot() {
		executor := tool.Executor
		if a.directToolExecutionEvents {
			executor = a.observedDirectToolExecutor(tool.Name, executor)
		}
		executors[tool.Name] = executor
	}
	return executors
}

// observedDirectToolExecutor is the execution-side half of the tool-call
// contract. It runs inside the bridge registry, so it observes a call only
// when the host handler truly runs and always has the actual result/error.
func (a *Agent) observedDirectToolExecutor(name string, executor ToolExecutor) ToolExecutor {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		// A wrapped Run already receives the CLI transcript's canonical tool
		// intent/result pair. Bridge receipts are the missing source only for a
		// retained turn delivered directly to the provider between Runs. Emitting
		// both sources during an active Run would render every ordinary tool twice.
		if a.isTurnInFlight() {
			return executor(ctx, args)
		}
		arguments, err := json.Marshal(args)
		if err != nil {
			arguments = []byte(`{"_serialization_error":"could not encode tool arguments"}`)
		}
		callID := "direct-" + uuid.NewString()
		start := events.NewToolCallStartEvent(0, name, events.ToolParams{Arguments: string(arguments)}, "direct_execution", "")
		start.ToolCallID = callID
		a.emitTypedEvent(ctx, start)

		started := time.Now()
		result, callErr := executor(ctx, args)
		duration := time.Since(started)
		if callErr != nil {
			failure := events.NewToolCallErrorEvent(0, name, callErr.Error(), "direct_execution", duration)
			failure.ToolCallID = callID
			a.emitTypedEvent(ctx, failure)
			return result, callErr
		}
		end := events.NewToolCallEndEvent(0, name, result, "direct_execution", duration, "")
		end.ToolCallID = callID
		a.emitTypedEvent(ctx, end)
		return result, nil
	}
}
