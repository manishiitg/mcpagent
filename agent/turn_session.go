package mcpagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type turnPolicyContextKey struct{}

// ToolPolicy is the authorization view for one turn. An empty AllowedTools
// slice means every tool in the immutable definition is allowed.
type ToolPolicy struct {
	AllowedTools []string
}

// Turn contains the user input, optional prior history, and runtime policy for
// one model turn. Runtime policy is deliberately not part of AgentDefinition.
type Turn struct {
	Input      string
	History    []llmtypes.MessageContent
	ToolPolicy ToolPolicy
}

// Usage is the structured accounting returned with every completed turn.
type Usage struct {
	PromptTokens         int
	CompletionTokens     int
	TotalTokens          int
	CacheTokens          int
	ReasoningTokens      int
	LLMCalls             int
	CacheEnabledLLMCalls int
	InputCostUSD         float64
	OutputCostUSD        float64
	ReasoningCostUSD     float64
	CacheCostUSD         float64
	TotalCostUSD         float64
	ContextUsagePercent  float64
}

// Result contains the completed output and continuation state for a turn.
type Result struct {
	Text    string
	History []llmtypes.MessageContent
	Handle  *AgentSessionHandle
	Usage   Usage
}

// DeliveryResult reports whether input was delivered into an active turn or
// queued for the next provider boundary.
type DeliveryResult struct {
	Queued bool
}

// ToolDefinitionView is the read-only diagnostic form of one registered tool.
type ToolDefinitionView struct {
	Name         string
	Description  string
	Source       string
	DisplayGroup string
}

// AgentDefinitionView exposes identity without mutable schemas or executors.
type AgentDefinitionView struct {
	Instructions     string
	Skills           []string
	SkillDefinitions []*llmtypes.Skill
	Tools            []ToolDefinitionView
	Observers        []AgentEventListener
}

// Session owns history, continuation, steering, and event access. Run calls on
// one session are serialized; callers use separate sessions for concurrency.
type Session struct {
	agent   *Agent
	mu      sync.Mutex
	history []llmtypes.MessageContent
	closed  bool
}

// Start opens a stateful session over this immutable agent definition.
func (a *Agent) Start(context.Context) (*Session, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	return &Session{agent: a}, nil
}

// Run is the one-turn convenience API. Use Start when history must persist
// across multiple turns.
func (a *Agent) Run(ctx context.Context, turn Turn) (Result, error) {
	session, err := a.Start(ctx)
	if err != nil {
		return Result{}, err
	}
	return session.Run(ctx, turn)
}

// Definition returns a read-only snapshot of the current immutable identity.
func (a *Agent) Definition() AgentDefinitionView {
	instructions := a.systemPrompt
	if a.definition != nil {
		instructions = a.definition.Instructions
	}
	for _, supplement := range a.appendedSystemPrompts {
		if strings.TrimSpace(supplement) == "" || strings.Contains(instructions, supplement) {
			continue
		}
		if strings.TrimSpace(instructions) == "" {
			instructions = supplement
		} else {
			instructions += "\n\n" + supplement
		}
	}
	view := AgentDefinitionView{Instructions: instructions}
	a.mu.RLock()
	view.Observers = append(view.Observers, a.listeners...)
	a.mu.RUnlock()
	for _, skill := range a.attachedSkills {
		if skill != nil && skill.Name != "" {
			view.Skills = append(view.Skills, skill.Name)
			view.SkillDefinitions = append(view.SkillDefinitions, cloneSkill(skill))
		}
	}
	registry, err := a.canonicalRegistry()
	if err == nil {
		for _, tool := range registry.snapshot() {
			description := ""
			if tool.Definition.Function != nil {
				description = tool.Definition.Function.Description
			}
			view.Tools = append(view.Tools, ToolDefinitionView{
				Name:         tool.Name,
				Description:  description,
				Source:       tool.Source,
				DisplayGroup: tool.DisplayGroup,
			})
		}
	}
	sort.Strings(view.Skills)
	return view
}

// Run executes one turn using the supplied runtime policy.
func (s *Session) Run(ctx context.Context, turn Turn) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Result{}, fmt.Errorf("session is closed")
	}
	policy, err := normalizeToolPolicy(turn.ToolPolicy)
	if err != nil {
		return Result{}, err
	}
	ctx = context.WithValue(ctx, turnPolicyContextKey{}, policy)

	history := s.history
	if len(history) == 0 && len(turn.History) > 0 {
		history = append([]llmtypes.MessageContent(nil), turn.History...)
	}
	if strings.TrimSpace(turn.Input) != "" {
		history = append(history, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: turn.Input}},
		})
	}
	if len(history) == 0 {
		return Result{}, fmt.Errorf("turn input or history is required")
	}

	allowed := policy.allowedMap()
	if s.agent.SessionID != "" {
		codeexec.SetSessionToolAllowList(s.agent.SessionID, allowed)
	}

	var text string
	var updated []llmtypes.MessageContent
	if handle := s.agent.currentAgentSessionHandle(); handle != nil && !handle.Provider.Empty() {
		text, updated, _, err = s.agent.continueAgentSessionWithHistory(ctx, handle, history)
	} else {
		text, updated, err = s.agent.AskWithHistory(ctx, history)
	}
	if len(updated) > 0 {
		s.history = append([]llmtypes.MessageContent(nil), updated...)
	}
	prompt, completion, total, cache, reasoning, calls, cacheCalls,
		inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsage := s.agent.GetTokenUsageWithPricing()
	result := Result{
		Text:    text,
		History: append([]llmtypes.MessageContent(nil), updated...),
		Handle:  s.agent.currentAgentSessionHandle(),
		Usage: Usage{
			PromptTokens:         prompt,
			CompletionTokens:     completion,
			TotalTokens:          total,
			CacheTokens:          cache,
			ReasoningTokens:      reasoning,
			LLMCalls:             calls,
			CacheEnabledLLMCalls: cacheCalls,
			InputCostUSD:         inputCost,
			OutputCostUSD:        outputCost,
			ReasoningCostUSD:     reasoningCost,
			CacheCostUSD:         cacheCost,
			TotalCostUSD:         totalCost,
			ContextUsagePercent:  contextUsage,
		},
	}
	return result, err
}

// Send queues steering input for the active provider turn.
func (s *Session) Send(_ context.Context, input string) (DeliveryResult, error) {
	if strings.TrimSpace(input) == "" {
		return DeliveryResult{}, fmt.Errorf("delivery input is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return DeliveryResult{}, fmt.Errorf("session is closed")
	}
	s.agent.AddSteerMessage(input)
	return DeliveryResult{Queued: true}, nil
}

func (s *Session) Snapshot() *AgentSessionHandle {
	return s.agent.currentAgentSessionHandle()
}

func (s *Session) Events() <-chan *events.AgentEvent {
	stream, _ := s.agent.getEventStream()
	return stream
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.history = nil
	return nil
}

type normalizedToolPolicy struct {
	allowed map[string]bool
}

func normalizeToolPolicy(policy ToolPolicy) (normalizedToolPolicy, error) {
	if len(policy.AllowedTools) == 0 {
		return normalizedToolPolicy{}, nil
	}
	allowed := make(map[string]bool, len(policy.AllowedTools))
	for _, raw := range policy.AllowedTools {
		name := strings.TrimSpace(raw)
		if name == "" {
			return normalizedToolPolicy{}, fmt.Errorf("tool policy contains an empty name")
		}
		if name != raw {
			return normalizedToolPolicy{}, fmt.Errorf("tool policy name %q has surrounding whitespace", raw)
		}
		allowed[name] = true
	}
	return normalizedToolPolicy{allowed: allowed}, nil
}

func (p normalizedToolPolicy) allows(name string) bool {
	return p.allowed == nil || p.allowed[name]
}

func (p normalizedToolPolicy) allowedMap() map[string]bool {
	if p.allowed == nil {
		return nil
	}
	result := make(map[string]bool, len(p.allowed))
	for name := range p.allowed {
		result[name] = true
	}
	return result
}

func toolPolicyFromContext(ctx context.Context) (normalizedToolPolicy, bool) {
	if ctx == nil {
		return normalizedToolPolicy{}, false
	}
	policy, ok := ctx.Value(turnPolicyContextKey{}).(normalizedToolPolicy)
	return policy, ok
}
