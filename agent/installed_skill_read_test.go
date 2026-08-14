package mcpagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Without a resolver, read_skill must behave exactly as it did before: attached
// skills only. This change is additive, so no case that worked may start
// failing and no new one may start succeeding.
func TestReadSkillWithoutResolverStaysAttachedOnly(t *testing.T) {
	a := &Agent{}
	a.attachedSkills = []*llmtypes.Skill{{Name: "router", Description: "d", Content: "body"}}

	if _, err := a.readOneAttachedSkill("router", ""); err != nil {
		t.Fatalf("attached skill must still be readable: %v", err)
	}
	_, err := a.readOneAttachedSkill("specialist", "")
	if err == nil {
		t.Fatal("an unattached skill must fail when no resolver is installed")
	}
	if !strings.Contains(err.Error(), "attached skill") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Progressive disclosure attaches a router and leaves specialists on disk.
// Reaching one previously meant knowing a filesystem path and shelling out.
func TestReadSkillFallsBackToAnInstalledSkill(t *testing.T) {
	a := &Agent{}
	a.attachedSkills = []*llmtypes.Skill{{Name: "router", Description: "d", Content: "body"}}
	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		if name != "specialist" {
			return InstalledSkillFile{}, fmt.Errorf("not installed: %s", name)
		}
		return InstalledSkillFile{
			Content:        "specialist body for " + relPath,
			Description:    "the specialist",
			AvailableFiles: []string{"SKILL.md", "references/deep.md"},
		}, nil
	})

	result, err := a.readOneAttachedSkill("specialist", "")
	if err != nil {
		t.Fatalf("installed skill should resolve: %v", err)
	}
	if result.Source != "installed" {
		t.Fatalf("Source = %q, want \"installed\" so the caller can tell it was read on demand", result.Source)
	}
	if result.Path != "SKILL.md" || !strings.Contains(result.Content, "SKILL.md") {
		t.Fatalf("default path should be SKILL.md, got %+v", result)
	}

	// The attached skill must still win, so attaching a skill never changes
	// where its content comes from.
	attached, err := a.readOneAttachedSkill("router", "")
	if err != nil || attached.Source != "attached" {
		t.Fatalf("attached skill must resolve from memory, got %+v err=%v", attached, err)
	}
}

// "not attached" and "not installed" need different fixes; collapsing them
// sends the reader to the wrong one.
func TestReadSkillDistinguishesNotAttachedFromNotInstalled(t *testing.T) {
	a := &Agent{}
	a.attachedSkills = []*llmtypes.Skill{{Name: "router", Content: "b"}}
	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		return InstalledSkillFile{}, fmt.Errorf("file not found")
	})
	_, err := a.readOneAttachedSkill("ghost", "")
	if err == nil {
		t.Fatal("a skill that is neither attached nor installed must error")
	}
	for _, want := range []string{"not attached", "workspace", "router"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q so the fix is obvious: %v", want, err)
		}
	}
}

// Path traversal must be rejected before the resolver is consulted — the host
// resolver should never be handed ../ to interpret.
func TestReadSkillRejectsUnsafePathsBeforeConsultingTheResolver(t *testing.T) {
	a := &Agent{}
	consulted := false
	a.SetInstalledSkillResolver(func(name, relPath string) (InstalledSkillFile, error) {
		consulted = true
		return InstalledSkillFile{Content: "x"}, nil
	})
	if _, err := a.readOneAttachedSkill("specialist", "../../etc/passwd"); err == nil {
		t.Fatal("an escaping path must be rejected")
	}
	if consulted {
		t.Fatal("the resolver must never be asked to interpret an unsafe path")
	}
}
