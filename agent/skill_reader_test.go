package mcpagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAttachedSkillAutomaticallyRegistersTransportNeutralReader(t *testing.T) {
	agent := &Agent{}
	if err := agent.attachSkill(&llmtypes.Skill{
		Name:        "workflow-reference",
		Description: "workflow contracts",
		Content:     "main skill instructions",
		SupportingFiles: []llmtypes.SkillFile{
			{RelPath: "references/stores.md", Content: []byte("store contract")},
			{RelPath: "assets/pixel.bin", Content: []byte{0xff, 0x00, 0x81}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	view := agent.Definition()
	found := false
	for _, tool := range view.Tools {
		if tool.Name == readSkillToolName {
			found = true
			if tool.DisplayGroup != readSkillToolCategory {
				t.Fatalf("read_skill display group = %q", tool.DisplayGroup)
			}
		}
	}
	if !found {
		t.Fatalf("attached skill did not register %s: %#v", readSkillToolName, view.Tools)
	}
	if len(agent.additionalBridgeTools) != 1 || agent.additionalBridgeTools[0] != readSkillToolName {
		t.Fatalf("read_skill was not projected through the coding-agent MCP bridge: %v", agent.additionalBridgeTools)
	}

	executor := agent.getCustomToolExecutor(readSkillToolName)
	if executor == nil {
		t.Fatal("read_skill executor is missing")
	}
	read := func(args map[string]interface{}) attachedSkillReadResult {
		t.Helper()
		raw, err := executor(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		var got attachedSkillReadResult
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("decode read_skill result: %v\n%s", err, raw)
		}
		return got
	}

	main := read(map[string]interface{}{"skill_name": "workflow-reference"})
	if main.Path != "SKILL.md" || main.Content != "main skill instructions" || main.Encoding != "utf-8" {
		t.Fatalf("main skill result = %#v", main)
	}
	if strings.Join(main.AvailableFiles, ",") != "SKILL.md,assets/pixel.bin,references/stores.md" {
		t.Fatalf("available files = %v", main.AvailableFiles)
	}

	reference := read(map[string]interface{}{"skill_name": "workflow-reference", "path": "references/stores.md"})
	if reference.Content != "store contract" || reference.Encoding != "utf-8" {
		t.Fatalf("reference result = %#v", reference)
	}

	binary := read(map[string]interface{}{"skill_name": "workflow-reference", "path": "assets/pixel.bin"})
	if binary.Encoding != "base64" || binary.Content != base64.StdEncoding.EncodeToString([]byte{0xff, 0x00, 0x81}) {
		t.Fatalf("binary result = %#v", binary)
	}
}

func TestAttachedSkillReaderRejectsTraversalAndUnknownResources(t *testing.T) {
	agent := &Agent{}
	if err := agent.attachSkill(&llmtypes.Skill{Name: "safe", Content: "body"}); err != nil {
		t.Fatal(err)
	}
	executor := agent.getCustomToolExecutor(readSkillToolName)

	for _, args := range []map[string]interface{}{
		{"skill_name": "safe", "path": "../secret"},
		{"skill_name": "safe", "path": "/etc/passwd"},
		{"skill_name": "safe", "path": "references/missing.md"},
		{"skill_name": "missing"},
	} {
		if _, err := executor(context.Background(), args); err == nil {
			t.Fatalf("read_skill accepted invalid arguments: %#v", args)
		}
	}
}

func TestReadSkillIsReservedAndSurvivesToolFiltering(t *testing.T) {
	agent := &Agent{toolFilter: NewToolFilter(nil, nil, nil, nil, nil)}
	if err := agent.attachSkill(&llmtypes.Skill{Name: "identity", Content: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := agent.registerCustomTool(readSkillToolName, "override", map[string]interface{}{}, definitionNoopTool, "skill_tools"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("read_skill override error = %v", err)
	}

	agent.toolAllowList = map[string]bool{"some_other_tool": true}
	filtered := agent.applyToolAllowList(agent.tools)
	if len(filtered) != 1 || filtered[0].Function == nil || filtered[0].Function.Name != readSkillToolName {
		t.Fatalf("intrinsic read_skill was filtered out: %#v", filtered)
	}
	policy, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"some_other_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, policy)
	if !agent.isToolAllowedForContext(ctx, readSkillToolName) {
		t.Fatal("turn policy blocked intrinsic read_skill")
	}
}

func TestNoAttachedSkillsMeansNoReadSkillTool(t *testing.T) {
	agent := &Agent{toolRegistry: newCanonicalToolRegistry()}
	if _, ok := findDefinitionTool(agent, readSkillToolName); ok {
		t.Fatal("read_skill should not exist without attached skills")
	}
}
