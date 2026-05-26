package mcpagent

import (
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Skills are Anthropic-format SKILL.md bundles attached to an Agent. The
// Skill / SkillFile / SkillSource value types live in llmtypes so adapters
// in multi-llm-provider-go can reference them without importing mcpagent
// (which would be a circular import). mcpagent owns the attachment API
// (AttachSkill / AttachedSkills / DetachSkill / ClearSkills) and the
// SkillProjector contract that adapters implement.

// AttachSkill registers a skill on the agent. Idempotent on Name:
// attaching a skill whose Name already exists replaces the prior entry.
// The skill becomes visible to transports through AttachedSkills.
func (a *Agent) AttachSkill(skill *llmtypes.Skill) {
	if a == nil || skill == nil || skill.Name == "" {
		return
	}
	for i, existing := range a.attachedSkills {
		if existing != nil && existing.Name == skill.Name {
			a.attachedSkills[i] = skill
			return
		}
	}
	a.attachedSkills = append(a.attachedSkills, skill)
}

// AttachedSkills returns the current list of skills attached to this
// agent. Transports read this at session launch (and at resume) to
// decide what to project to disk or list in the system prompt. The
// returned slice is a shallow copy; callers must not mutate skill
// values in place.
func (a *Agent) AttachedSkills() []*llmtypes.Skill {
	if a == nil || len(a.attachedSkills) == 0 {
		return nil
	}
	out := make([]*llmtypes.Skill, len(a.attachedSkills))
	copy(out, a.attachedSkills)
	return out
}

// DetachSkill removes a skill by name. No-op if no skill with that name
// is attached.
func (a *Agent) DetachSkill(name string) {
	if a == nil || name == "" {
		return
	}
	for i, existing := range a.attachedSkills {
		if existing != nil && existing.Name == name {
			a.attachedSkills = append(a.attachedSkills[:i], a.attachedSkills[i+1:]...)
			return
		}
	}
}

// ClearSkills removes every attached skill. Used at session reset and
// before re-attaching a fresh skill set (e.g., on workshop-mode change).
func (a *Agent) ClearSkills() {
	if a == nil {
		return
	}
	a.attachedSkills = nil
}

// SkillProjector is the contract a transport adapter implements when it
// wants to project skills to the provider's working directory at session
// launch. Adapters that only need the system-prompt listing (the API
// transports) do not implement this interface.
//
// ProjectSkills must be idempotent: it is called both at launch and at
// resume, and the content is typically identical between calls.
//
// workdir is the absolute path of the provider's working directory (the
// per-session workspace where the adapter already writes rules and
// hooks). Adapters compute their own native subdirectory beneath workdir
// (e.g., ".claude/skills/" for claude-code, ".agents/skills/" for
// everyone else).
type SkillProjector interface {
	ProjectSkills(workdir string, skills []*llmtypes.Skill) error
}

// renderSkillListing produces the system-prompt section that announces
// the attached skills to the model. Format follows the progressive-
// disclosure pattern Anthropic skills use: every skill's name +
// description is included up front (~50-100 tokens each) and the model
// reads the full SKILL.md body only when it decides the skill is
// relevant.
//
// On CLI transports the SKILL.md files are also projected to disk
// (.claude/skills/, .agents/skills/) by the adapter. The listing is
// redundant there but harmless — and acts as defense-in-depth if
// projection ever fails.
//
// Returns an empty string when no skills are attached.
func renderSkillListing(skills []*llmtypes.Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	b.WriteString("The following skills are attached to this session. Each skill extends your capabilities with specialized instructions and (optionally) supporting files. ")
	b.WriteString("When a skill is relevant to what the user is asking, read the full SKILL.md body before acting on it.\n\n")
	for _, s := range skills {
		if s == nil || s.Name == "" {
			continue
		}
		fmt.Fprintf(&b, "- **%s**", s.Name)
		if d := strings.TrimSpace(s.Description); d != "" {
			fmt.Fprintf(&b, ": %s", d)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
