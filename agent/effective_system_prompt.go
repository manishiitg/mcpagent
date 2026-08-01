package mcpagent

import (
	"context"
	"strings"

	"github.com/manishiitg/mcpagent/agent/prompt"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const (
	availableToolsOpenTag  = "<available_tools>"
	availableToolsCloseTag = "</available_tools>"
	preDiscoveredOpenTag   = "<pre_discovered_tool_specs>"
	preDiscoveredCloseTag  = "</pre_discovered_tool_specs>"
	effectiveToolsMarker   = "\x00mcpagent-effective-tools\x00"
)

// effectiveSystemPrompt composes instructions with the current, authorized
// tool manifest at the final read boundary. The stored systemPrompt is not a
// source of tool truth: registration and allow-list state can change after it
// was set, and custom prompts can replace it entirely.
func (a *Agent) effectiveSystemPromptForContext(ctx context.Context) string {
	instructions := a.systemPrompt
	for _, supplement := range a.appendedSystemPrompts {
		if instructions == "" {
			instructions = supplement
			continue
		}
		instructions = prompt.NormalizeForAppend(instructions) + "\n\n" + supplement
	}
	return a.composeEffectiveSystemPromptForContext(ctx, instructions)
}

// outgoingSystemPrompt is the exact instruction string placed on outbound
// model requests and mirrored into prompt events/debug output.
func (a *Agent) outgoingSystemPrompt() string {
	return a.outgoingSystemPromptForContext(context.Background())
}

func (a *Agent) outgoingSystemPromptForContext(ctx context.Context) string {
	systemPrompt := a.effectiveSystemPromptForContext(ctx)
	if listing := renderSkillListing(a.attachedSkills); listing != "" {
		if systemPrompt != "" {
			return systemPrompt + "\n\n" + listing
		}
		return listing
	}
	return systemPrompt
}

// composeEffectiveSystemPrompt is shared by actual send paths, exported prompt
// reads, and prompt-event/log rendering. This keeps what operators inspect
// identical to what the model receives.
func (a *Agent) composeEffectiveSystemPromptForContext(ctx context.Context, base string) string {
	if !a.UseCodeExecutionMode {
		return strings.ReplaceAll(base, prompt.ToolStructurePlaceholder, "")
	}

	toolStructure, err := a.buildToolIndexForContext(ctx)
	if err != nil {
		if a.Logger != nil {
			a.Logger.Warn("Failed to build request-time tool manifest", loggerv2.Error(err))
		}
		toolStructure = ""
	}
	section := prompt.BuildAvailableToolsSection(toolStructure, a.buildPreDiscoveredToolSpecsForContext(ctx))
	return replaceEffectiveToolsSection(base, section)
}

// replaceEffectiveToolsSection replaces a placeholder or tagged manifest in
// place and otherwise appends one. It removes every stale tagged copy first, so
// retries and prompt overwrites are idempotent and always leave balanced tags.
func replaceEffectiveToolsSection(base, section string) string {
	working := base

	if strings.Contains(working, prompt.ToolStructurePlaceholder) {
		working = strings.Replace(working, prompt.ToolStructurePlaceholder, effectiveToolsMarker, 1)
		working = strings.ReplaceAll(working, prompt.ToolStructurePlaceholder, "")
	} else if start, end, ok := firstTaggedRange(working, availableToolsOpenTag, availableToolsCloseTag); ok {
		working = working[:start] + effectiveToolsMarker + working[end:]
	} else {
		working = strings.TrimRight(working, "\n") + "\n\n" + effectiveToolsMarker
	}

	working = removeTaggedSections(working, availableToolsOpenTag, availableToolsCloseTag)
	working = removeTaggedSections(working, preDiscoveredOpenTag, preDiscoveredCloseTag)
	working = strings.Replace(working, effectiveToolsMarker, section, 1)
	return strings.TrimSpace(working)
}

func firstTaggedRange(input, openTag, closeTag string) (int, int, bool) {
	start := strings.Index(input, openTag)
	if start < 0 {
		return 0, 0, false
	}
	relEnd := strings.Index(input[start+len(openTag):], closeTag)
	if relEnd < 0 {
		return 0, 0, false
	}
	end := start + len(openTag) + relEnd + len(closeTag)
	return start, end, true
}

func removeTaggedSections(input, openTag, closeTag string) string {
	for {
		start, end, ok := firstTaggedRange(input, openTag, closeTag)
		if !ok {
			return input
		}
		input = input[:start] + input[end:]
	}
}
