package mcpagent

import (
	"context"
	"strings"

	"github.com/manishiitg/mcpagent/agent/prompt"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	llm "github.com/manishiitg/multi-llm-provider-go"
)

const (
	availableToolsOpenTag  = "<available_tools>"
	availableToolsCloseTag = "</available_tools>"
	effectiveToolsMarker   = "\x00mcpagent-effective-tools\x00"
)

// effectiveSystemPrompt composes instructions with the current, authorized
// tool manifest at the final read boundary. The stored systemPrompt is not a
// source of tool truth: registration and allow-list state can change after it
// was set, and custom prompts can replace it entirely.
func (a *Agent) effectiveSystemPromptForContext(ctx context.Context) string {
	// systemPrompt is always the caller/product-owned base. Everything
	// mcpagent discovers or contributes (MCP guidance, runtime routing, tools,
	// skills) is an extension and must never replace that identity.
	instructions := composeInstructionExtensions(a.systemPrompt, a.appendedSystemPrompts...)
	return a.composeEffectiveSystemPromptForContext(ctx, instructions)
}

// composeInstructionExtensions preserves the base as the first instruction
// block and appends non-empty extensions in order. Keeping this composition in
// one helper prevents inspection and send paths from quietly disagreeing about
// whether an mcpagent-owned block replaces a product prompt.
func composeInstructionExtensions(base string, extensions ...string) string {
	result := strings.TrimSpace(base)
	for _, extension := range extensions {
		extension = strings.TrimSpace(extension)
		if extension == "" {
			continue
		}
		if result == "" {
			result = extension
			continue
		}
		result = prompt.NormalizeForAppend(result) + "\n\n" + extension
	}
	return result
}

// outgoingSystemPrompt is the exact instruction string placed on outbound
// model requests and mirrored into prompt events/debug output.
func (a *Agent) outgoingSystemPrompt() string {
	return a.outgoingSystemPromptForContext(context.Background())
}

func (a *Agent) outgoingSystemPromptForContext(ctx context.Context) string {
	systemPrompt := a.effectiveSystemPromptForContext(ctx)
	// Coding CLIs receive the same attached skills through their native on-disk
	// skill projection. Repeating the catalog inside AGENTS.md/CLAUDE.md makes
	// the CLI discover every skill twice. API models have no native projection,
	// so they still need the prompt listing.
	if !llm.IsCodingAgentProvider(a.provider, a.modelID) {
		if listing := renderSkillListing(a.attachedSkills); listing != "" {
			if systemPrompt != "" {
				return systemPrompt + "\n\n" + listing
			}
			return listing
		}
	}
	return systemPrompt
}

// composeEffectiveSystemPrompt is shared by actual send paths, exported prompt
// reads, and prompt-event/log rendering. This keeps what operators inspect
// identical to what the model receives.
func (a *Agent) composeEffectiveSystemPromptForContext(ctx context.Context, base string) string {
	if !a.useCodeExecutionMode {
		return strings.ReplaceAll(base, prompt.ToolStructurePlaceholder, "")
	}

	toolStructure, err := a.buildToolIndexForContext(ctx)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("Failed to build request-time tool manifest", loggerv2.Error(err))
		}
		toolStructure = ""
	}
	section := prompt.BuildAvailableToolsSection(toolStructure)
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
