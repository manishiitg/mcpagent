package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type definitionAssemblyObserver struct{}

func (definitionAssemblyObserver) HandleEvent(context.Context, *events.AgentEvent) error { return nil }
func (definitionAssemblyObserver) Name() string                                          { return "definition-assembly-test" }

func TestDefinitionAssemblyIsCachedAndRejectsChangesAfterSeal(t *testing.T) {
	draft := &Agent{}
	first := NewDefinitionAssembly(draft)
	second := NewDefinitionAssembly(draft)
	if first != second {
		t.Fatal("NewDefinitionAssembly returned different lifecycles for one draft")
	}
	if err := first.AddInstructions("before seal"); err != nil {
		t.Fatal(err)
	}
	first.Seal()
	if err := AddDefinitionInstructions(draft, "after seal"); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("post-seal mutation error = %v, want sealed", err)
	}
}

func TestDefinitionAssemblyBuildFreezesCompleteIdentity(t *testing.T) {
	ctx := context.Background()
	runtime := RuntimeConfig{
		Model: &providerKeyCarrierModel{},
		LegacyOptions: []AgentOption{
			WithProvider(llm.ProviderOpenAI),
		},
	}
	draft, err := NewAgentFromDefinition(ctx, AgentDefinition{Instructions: "base"}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer draft.Close()

	assembly := NewDefinitionAssembly(draft)
	if err := assembly.AddInstructions("supplement"); err != nil {
		t.Fatal(err)
	}
	if err := assembly.AddSkill(&llmtypes.Skill{Name: "review"}); err != nil {
		t.Fatal(err)
	}
	if err := assembly.AddTool("inspect", "inspect data", map[string]interface{}{"type": "object"}, definitionNoopTool, 0, "workflow"); err != nil {
		t.Fatal(err)
	}
	if err := assembly.AddObserver(definitionAssemblyObserver{}); err != nil {
		t.Fatal(err)
	}

	next, definition, err := assembly.Build(ctx, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if definition.Instructions != "base\n\nsupplement" {
		t.Fatalf("instructions = %q", definition.Instructions)
	}
	if len(definition.Skills) != 1 || definition.Skills[0].Name != "review" {
		t.Fatalf("skills = %#v", definition.Skills)
	}
	if len(definition.Tools.Direct) != 1 || definition.Tools.Direct[0].Name != "inspect" {
		t.Fatalf("tools = %#v", definition.Tools.Direct)
	}
	if got := next.Definition(); len(got.Observers) != 1 || len(got.Tools) != 1 {
		t.Fatalf("built definition view = %#v", got)
	}
	if err := AddDefinitionTool(next, "late", "late", nil, definitionNoopTool, "workflow"); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("built agent accepted late tool: %v", err)
	}
}

func TestStructuredOutputToolUsesTurnSignalWithoutRegistryMutation(t *testing.T) {
	tool, err := NewStructuredOutputTool(
		"submit_report",
		"submit the report",
		`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	called := make(chan bool, 1)
	canceled := false
	ctx := context.WithValue(context.Background(), structuredOutputSignalContextKey{}, structuredOutputSignal{
		toolName: tool.Name,
		called:   called,
		cancel:   func() { canceled = true },
	})
	if _, err := tool.Execute(ctx, map[string]interface{}{"summary": "done"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	default:
		t.Fatal("structured output tool did not signal completion")
	}
	if !canceled {
		t.Fatal("structured output tool did not cancel the completed turn")
	}
}
