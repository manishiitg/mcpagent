package mcpagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/retainedturn"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type turnPolicyContextKey struct{}
type turnStreamingCallbackContextKey struct{}

// ToolPolicy is the authorization view for one turn. An empty AllowedTools
// slice means every tool in the immutable definition is allowed.
type ToolPolicy struct {
	AllowedTools []string
}

// Turn contains the user input, optional prior history, and runtime policy for
// one model turn. Runtime policy is deliberately not part of AgentDefinition.
type Turn struct {
	// ID is the stable provider-neutral identity for this message. Session.Run
	// creates one when omitted.
	ID                string
	Input             string
	History           []llmtypes.MessageContent
	ToolPolicy        ToolPolicy
	StreamingCallback func(llmtypes.StreamChunk)
}

// Usage is the structured accounting returned with every completed turn.
type Usage struct {
	PromptTokens         int
	CompletionTokens     int
	TotalTokens          int
	CacheTokens          int
	CacheReadTokens      int
	CacheWriteTokens     int
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
	TurnID  string
	Text    string
	History []llmtypes.MessageContent
	Handle  *AgentSessionHandle
	Usage   Usage
}

// DeliveryResult reports whether input was delivered into an active turn or
// queued for the next provider boundary.
type DeliveryResult struct {
	TurnID    string
	Queued    bool
	Status    UserMessageDeliveryStatus
	Provider  llm.Provider
	Transport llm.CodingAgentTransport
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
}

// Session owns history, continuation, steering, and event access. Run calls on
// one session are serialized; callers use separate sessions for concurrency.
type Session struct {
	agent   *Agent
	runMu   sync.Mutex
	sendMu  sync.Mutex
	stateMu sync.Mutex
	history []llmtypes.MessageContent
	closed  bool

	// runActive distinguishes true mid-turn steering from a message submitted
	// between turns. retainedActive covers a directly-injected warm tmux turn:
	// it has no Session.Run caller, but it still owns one canonical completion
	// lifecycle and accepts later messages as steering into that same turn.
	runActive        bool
	retainedStarting bool
	retainedActive   bool
	retainedSeq      uint64
	activeTurn       *canonicalTurnLifecycle
	watchCtx         context.Context
	watchCancel      context.CancelFunc

	// Tests replace this on an individual Session. Production always reads the
	// provider adapter's authoritative retained transcript/sidecar.
	retainedFinalResponse func(llm.Provider, string, time.Time) string
}

// Start opens a stateful session over this immutable agent definition.
func (a *Agent) Start(context.Context) (*Session, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	session := &Session{
		agent:                 a,
		watchCtx:              watchCtx,
		watchCancel:           watchCancel,
		retainedFinalResponse: retainedturn.FinalResponse,
	}
	registerTurnSession(a.sessionID, session)
	return session, nil
}

// Run is the one-turn convenience API. Use Start when history must persist
// across multiple turns.
func (a *Agent) Run(ctx context.Context, turn Turn) (Result, error) {
	session, err := a.Start(ctx)
	if err != nil {
		return Result{}, err
	}
	// Run is explicitly the one-turn convenience API. It must not leave the
	// durable session registry holding this Agent after the result returns;
	// callers that need continuation use Start and own Session.Close.
	defer session.Close()
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
func (s *Session) Run(ctx context.Context, turn Turn) (result Result, err error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return Result{}, fmt.Errorf("session is closed")
	}
	if s.retainedStarting || s.retainedActive {
		s.stateMu.Unlock()
		return Result{}, ErrTurnAlreadyInFlight
	}
	s.runActive = true
	history := append([]llmtypes.MessageContent(nil), s.history...)
	s.stateMu.Unlock()
	if !s.agent.tryClaimTurnInFlight() {
		s.stateMu.Lock()
		s.runActive = false
		s.stateMu.Unlock()
		return Result{}, ErrTurnAlreadyInFlight
	}
	lifecycle := newCanonicalTurnLifecycle(turn.ID)
	turn.ID = lifecycle.id
	ctx = withCanonicalTurnLifecycle(ctx, lifecycle)
	s.stateMu.Lock()
	s.activeTurn = lifecycle
	s.stateMu.Unlock()
	defer func() {
		result.TurnID = lifecycle.id
		if !lifecycle.isTerminal() {
			status := "completed"
			completion := events.NewUnifiedCompletionEvent(
				"session", string(s.agent.agentMode), turn.Input, result.Text,
				status, time.Since(lifecycle.startedAt), 1,
			)
			completion.Metadata["source"] = "mcpagent_session"
			if err != nil {
				completion.Status = "error"
				completion.Error = err.Error()
			}
			s.agent.annotateUnifiedCompletionEvent(completion)
			s.agent.emitTypedEvent(ctx, completion)
		}
		s.agent.setTurnInFlight(false)
		s.stateMu.Lock()
		s.runActive = false
		if s.activeTurn == lifecycle {
			s.activeTurn = nil
		}
		s.stateMu.Unlock()
	}()
	policy, err := normalizeToolPolicy(turn.ToolPolicy)
	if err != nil {
		return Result{}, err
	}
	ctx = context.WithValue(ctx, turnPolicyContextKey{}, policy)
	if turn.StreamingCallback != nil {
		ctx = context.WithValue(ctx, turnStreamingCallbackContextKey{}, turn.StreamingCallback)
	}

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
	if allowed != nil && s.agent.isIntrinsicIdentityTool(readSkillToolName) {
		allowed[readSkillToolName] = true
	}
	if s.agent.sessionID != "" {
		codeexec.SetSessionToolAllowList(s.agent.sessionID, allowed)
	}

	var text string
	var updated []llmtypes.MessageContent
	handleBeforeTurn := s.agent.currentAgentSessionHandle()
	nativeSessionID := ""
	if handleBeforeTurn != nil {
		nativeSessionID = handleBeforeTurn.Provider.NativeSessionID
	}
	providerStartedAt := time.Now()
	if s.agent.logger != nil {
		s.agent.logger.Debug(fmt.Sprintf("[COMPLETION_TRACE] stage=mcpagent_provider_call_started session=%q provider=%q native_session=%q", s.agent.sessionID, s.agent.provider, nativeSessionID))
	}
	if handleBeforeTurn != nil && !handleBeforeTurn.Provider.Empty() {
		text, updated, _, err = s.agent.continueAgentSessionWithHistory(ctx, handleBeforeTurn, history)
	} else {
		text, updated, err = s.agent.askWithHistory(ctx, history)
	}
	if s.agent.logger != nil {
		outcome := "completed"
		if err != nil {
			outcome = "error"
		}
		handleAfterTurn := s.agent.currentAgentSessionHandle()
		if handleAfterTurn != nil && handleAfterTurn.Provider.NativeSessionID != "" {
			nativeSessionID = handleAfterTurn.Provider.NativeSessionID
		}
		s.agent.logger.Debug(fmt.Sprintf("[COMPLETION_TRACE] stage=mcpagent_provider_call_returned session=%q provider=%q native_session=%q outcome=%s elapsed=%s", s.agent.sessionID, s.agent.provider, nativeSessionID, outcome, time.Since(providerStartedAt).Round(time.Millisecond)))
	}
	if len(updated) > 0 {
		s.stateMu.Lock()
		s.history = append([]llmtypes.MessageContent(nil), updated...)
		s.stateMu.Unlock()
	}
	prompt, completion, total, cache, reasoning, calls, cacheCalls,
		inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsage := s.agent.getTokenUsageWithPricing()
	result = Result{
		TurnID:  lifecycle.id,
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

// Send submits input to the durable provider conversation. During Session.Run
// it steers the active turn. Between Runs it starts a retained tmux turn and
// owns that turn through its canonical final-response event; callers receive a
// fast delivery acknowledgement and do not need to scrape provider panes.
func (s *Session) Send(ctx context.Context, input string) (DeliveryResult, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return DeliveryResult{}, fmt.Errorf("session is closed")
	}
	wasActive := s.runActive || s.retainedActive
	lifecycle := s.activeTurn
	if !wasActive {
		s.retainedStarting = true
		lifecycle = newCanonicalTurnLifecycle("")
		s.activeTurn = lifecycle
	}
	s.stateMu.Unlock()
	if lifecycle != nil {
		ctx = withCanonicalTurnLifecycle(ctx, lifecycle)
	}
	clearStarting := func() {
		s.stateMu.Lock()
		s.retainedStarting = false
		if !s.runActive && !s.retainedActive && s.activeTurn == lifecycle {
			s.activeTurn = nil
		}
		s.stateMu.Unlock()
	}
	delivery, err := s.agent.deliverUserMessage(ctx, UserMessageDeliveryRequest{
		SessionID: s.agent.sessionID,
		Message:   input,
		Intent:    UserMessageDeliveryIntentAuto,
	})
	turnID := ""
	if lifecycle != nil {
		turnID = lifecycle.id
	}
	result := DeliveryResult{
		TurnID:    turnID,
		Queued:    delivery.DeliveryStatus == UserMessageDeliveryStatusQueuedForInjection,
		Status:    delivery.DeliveryStatus,
		Provider:  delivery.Provider,
		Transport: delivery.Transport,
	}
	if err != nil {
		clearStarting()
		return result, err
	}
	if !wasActive && delivery.DeliveryStatus == UserMessageDeliveryStatusSentToCLI && delivery.Transport == llm.CodingAgentTransportTmux {
		s.startRetainedCompletionWatch(lifecycle, input, delivery.Provider, delivery.Transport)
	} else if !wasActive {
		clearStarting()
	}
	return result, nil
}

const retainedCompletionPollInterval = 100 * time.Millisecond

func (s *Session) startRetainedCompletionWatch(lifecycle *canonicalTurnLifecycle, input string, provider llm.Provider, transport llm.CodingAgentTransport) {
	startedAt := time.Now()
	s.stateMu.Lock()
	s.retainedStarting = false
	if s.closed || s.retainedActive {
		s.stateMu.Unlock()
		return
	}
	s.retainedActive = true
	if lifecycle == nil {
		lifecycle = newCanonicalTurnLifecycle("")
	}
	s.activeTurn = lifecycle
	s.retainedSeq++
	seq := s.retainedSeq
	watchCtx := s.watchCtx
	reader := s.retainedFinalResponse
	s.stateMu.Unlock()
	if watchCtx == nil {
		watchCtx = context.Background()
	}
	if reader == nil {
		reader = retainedturn.FinalResponse
	}

	go func() {
		ticker := time.NewTicker(retainedCompletionPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				finalResult := strings.TrimSpace(reader(provider, s.agent.sessionID, startedAt))
				if finalResult == "" {
					continue
				}
				s.completeRetainedTurn(lifecycle, seq, input, finalResult, provider, transport, startedAt)
				return
			}
		}
	}()
}

func (s *Session) completeRetainedTurn(lifecycle *canonicalTurnLifecycle, seq uint64, input, finalResult string, provider llm.Provider, transport llm.CodingAgentTransport, startedAt time.Time) {
	s.stateMu.Lock()
	if s.closed || !s.retainedActive || s.retainedSeq != seq {
		s.stateMu.Unlock()
		return
	}
	s.retainedActive = false
	if s.activeTurn == lifecycle {
		s.activeTurn = nil
	}
	s.history = append(s.history,
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: input}}},
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: finalResult}}},
	)
	s.stateMu.Unlock()

	completion := events.NewUnifiedCompletionEvent("coding_agent", "retained", input, finalResult, "completed", time.Since(startedAt), 1)
	completion.Metadata["source"] = "mcpagent_session"
	completion.Metadata["provider"] = string(provider)
	completion.Metadata["transport"] = string(transport)
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), completion)
}

func (s *Session) Snapshot() *AgentSessionHandle {
	return s.agent.currentAgentSessionHandle()
}

func (s *Session) Events() <-chan *events.AgentEvent {
	stream, _ := s.agent.getEventStream()
	return stream
}

func (s *Session) Close() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.closed = true
	if s.watchCancel != nil {
		s.watchCancel()
	}
	s.retainedActive = false
	s.retainedStarting = false
	s.activeTurn = nil
	s.history = nil
	unregisterTurnSession(s)
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
