package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolExecutor is the implementation of a direct tool.
type ToolExecutor func(context.Context, map[string]interface{}) (string, error)

// AgentDefinition contains the three immutable identity inputs of an agent.
// NewAgentFromDefinition clones this value before creating runtime state.
type AgentDefinition struct {
	Instructions string
	Skills       []*llmtypes.Skill
	Tools        ToolSet
}

// ToolSet contains direct implementations and configured MCP tool sources.
// Tools from both sources share one model-facing namespace.
type ToolSet struct {
	Direct []ToolDefinition
	MCP    []MCPToolSource
}

// ToolDefinition describes one directly executed tool.
//
// DisplayGroup is optional presentation metadata. It is retained by the
// compatibility registry while categories are removed from authorization and
// addressing; callers and models must address the tool only by Name.
type ToolDefinition struct {
	Name         string
	Description  string
	InputSchema  map[string]interface{}
	Execute      ToolExecutor
	Timeout      time.Duration
	DisplayGroup string
}

// MCPToolSource selects a configured MCP server by its stable configuration
// name. Connection details and credentials remain runtime configuration.
type MCPToolSource struct {
	Name string
}

// RuntimeConfig contains infrastructure needed to operate an agent definition.
//
// LegacyOptions is a temporary migration bridge for existing runtime knobs. It
// is applied before the definition, so options cannot override instructions or
// MCP sources owned by the immutable definition. It will disappear as those
// knobs move behind Session and internal runtime services.
type RuntimeConfig struct {
	Model         llmtypes.Model
	MCPConfigPath string
	LegacyOptions []AgentOption
}

// NewAgentFromDefinition constructs an Agent from a validated, cloned identity.
// The returned concrete type still exposes the legacy surface while downstream
// callers migrate; identity assembly itself is atomic on this path.
func NewAgentFromDefinition(ctx context.Context, definition AgentDefinition, runtime RuntimeConfig) (*Agent, error) {
	if runtime.Model == nil {
		return nil, fmt.Errorf("runtime model cannot be nil")
	}

	definition, err := cloneAndValidateAgentDefinition(definition)
	if err != nil {
		return nil, err
	}

	options := append([]AgentOption(nil), runtime.LegacyOptions...)
	options = append(options, WithSystemPrompt(definition.Instructions))
	if len(definition.Tools.MCP) > 0 {
		names := make([]string, 0, len(definition.Tools.MCP))
		for _, source := range definition.Tools.MCP {
			names = append(names, source.Name)
		}
		options = append(options, WithServerName(strings.Join(names, ",")))
	}

	agent, err := NewAgent(ctx, runtime.Model, runtime.MCPConfigPath, options...)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*Agent, error) {
		agent.Close()
		return nil, cause
	}

	for _, skill := range definition.Skills {
		agent.AttachSkill(skill)
	}
	for _, tool := range definition.Tools.Direct {
		group := strings.TrimSpace(tool.DisplayGroup)
		if group == "" {
			group = "custom"
		}
		if err := agent.RegisterCustomToolWithTimeout(
			tool.Name,
			tool.Description,
			tool.InputSchema,
			tool.Execute,
			tool.Timeout,
			group,
		); err != nil {
			return fail(fmt.Errorf("register direct tool %q: %w", tool.Name, err))
		}
	}
	agent.definition = &definition

	return agent, nil
}

func cloneAndValidateAgentDefinition(input AgentDefinition) (AgentDefinition, error) {
	result := AgentDefinition{Instructions: input.Instructions}

	result.Skills = make([]*llmtypes.Skill, 0, len(input.Skills))
	seenSkills := make(map[string]struct{}, len(input.Skills))
	for i, skill := range input.Skills {
		if skill == nil {
			return AgentDefinition{}, fmt.Errorf("skill %d is nil", i)
		}
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			return AgentDefinition{}, fmt.Errorf("skill %d has an empty name", i)
		}
		if _, exists := seenSkills[name]; exists {
			return AgentDefinition{}, fmt.Errorf("duplicate skill name %q", name)
		}
		seenSkills[name] = struct{}{}
		result.Skills = append(result.Skills, cloneSkill(skill))
	}

	result.Tools.MCP = make([]MCPToolSource, 0, len(input.Tools.MCP))
	seenSources := make(map[string]struct{}, len(input.Tools.MCP))
	for i, source := range input.Tools.MCP {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			return AgentDefinition{}, fmt.Errorf("MCP tool source %d has an empty name", i)
		}
		if name != source.Name {
			return AgentDefinition{}, fmt.Errorf("MCP tool source %q has surrounding whitespace", source.Name)
		}
		if _, exists := seenSources[name]; exists {
			return AgentDefinition{}, fmt.Errorf("duplicate MCP tool source %q", name)
		}
		seenSources[name] = struct{}{}
		result.Tools.MCP = append(result.Tools.MCP, MCPToolSource{Name: name})
	}

	result.Tools.Direct = make([]ToolDefinition, 0, len(input.Tools.Direct))
	seenTools := make(map[string]struct{}, len(input.Tools.Direct))
	for i, tool := range input.Tools.Direct {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return AgentDefinition{}, fmt.Errorf("direct tool %d has an empty name", i)
		}
		if name != tool.Name {
			return AgentDefinition{}, fmt.Errorf("direct tool %q has surrounding whitespace", tool.Name)
		}
		if tool.Execute == nil {
			return AgentDefinition{}, fmt.Errorf("direct tool %q has no executor", name)
		}
		if _, exists := seenTools[name]; exists {
			return AgentDefinition{}, fmt.Errorf("duplicate direct tool name %q", name)
		}
		seenTools[name] = struct{}{}
		result.Tools.Direct = append(result.Tools.Direct, ToolDefinition{
			Name:         name,
			Description:  tool.Description,
			InputSchema:  cloneStringAnyMap(tool.InputSchema),
			Execute:      tool.Execute,
			Timeout:      tool.Timeout,
			DisplayGroup: tool.DisplayGroup,
		})
	}

	return result, nil
}

func cloneSkill(input *llmtypes.Skill) *llmtypes.Skill {
	result := *input
	result.Paths = append([]string(nil), input.Paths...)
	if input.Metadata != nil {
		result.Metadata = make(map[string]string, len(input.Metadata))
		for key, value := range input.Metadata {
			result.Metadata[key] = value
		}
	}
	result.SupportingFiles = make([]llmtypes.SkillFile, len(input.SupportingFiles))
	for i, file := range input.SupportingFiles {
		result.SupportingFiles[i] = file
		result.SupportingFiles[i].Content = append([]byte(nil), file.Content...)
	}
	return &result
}

func cloneStringAnyMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	result := make(map[string]interface{}, len(input))
	for key, value := range input {
		result[key] = cloneJSONLikeValue(value)
	}
	return result
}

func cloneJSONLikeValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneStringAnyMap(typed)
	case []interface{}:
		result := make([]interface{}, len(typed))
		for i, item := range typed {
			result[i] = cloneJSONLikeValue(item)
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
