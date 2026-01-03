package mcpagent

import (
	"context"
	"errors"
	"fmt"
	"mcpagent/events"
	"mcpagent/llm"
	loggerv2 "mcpagent/logger/v2"
	"mcpagent/observability"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// isContextCanceledError checks if an error is due to context cancellation or deadline exceeded
func isContextCanceledError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "context canceled") ||
		strings.Contains(err.Error(), "context deadline exceeded")
}

// retryOriginalModel handles retry logic for throttling and zero_candidates errors
// Returns: shouldRetry (bool), delay (time.Duration), error
func retryOriginalModel(a *Agent, ctx context.Context, errorType string, attempt, maxRetries int, baseDelay, maxDelay time.Duration, turn int, logger loggerv2.Logger, usage observability.UsageMetrics) (bool, time.Duration, error) {
	delay := time.Duration(float64(baseDelay) * (1.5 + float64(attempt)*0.5))
	if delay > maxDelay {
		delay = maxDelay
	}

	// Emit retry attempt event with proper model/provider info for UI display
	retryAttemptEvent := events.NewFallbackAttemptEvent(
		turn, attempt+1, maxRetries,
		a.ModelID, string(a.provider), "retry", // Use "retry" phase to distinguish from actual fallbacks
		false, delay, fmt.Sprintf("%s - retrying original model", errorType),
	)
	a.EmitTypedEvent(ctx, retryAttemptEvent)

	var logMsg string
	if errorType == "zero_candidates_error" {
		logMsg = fmt.Sprintf("🔄 [ZERO_CANDIDATES] Retrying original model FIRST (before fallbacks). Waiting %v before retry (attempt %d/%d)...", delay, attempt+1, maxRetries)
	} else {
		logMsg = fmt.Sprintf("🔄 [THROTTLING] Retrying original model FIRST (before fallbacks). Waiting %v before retry (attempt %d/%d)...", delay, attempt+1, maxRetries)
	}
	logger.Info(logMsg)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	// Wait for delay or context cancellation
	select {
	case <-ctx.Done():
		return false, delay, ctx.Err()
	case <-timer.C:
	}

	var retryLogMsg string
	if errorType == "zero_candidates_error" {
		retryLogMsg = fmt.Sprintf("🔄 [ZERO_CANDIDATES] Retrying with original model (turn %d, attempt %d/%d)...", turn, attempt+2, maxRetries)
	} else {
		retryLogMsg = fmt.Sprintf("🔄 [THROTTLING] Retrying with original model (turn %d, attempt %d/%d)...", turn, attempt+2, maxRetries)
	}
	logger.Info(retryLogMsg)
	return true, delay, nil
}

// isMaxTokenError checks if an error is due to reaching maximum token limit
func isMaxTokenError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Exclude context cancellation from max token errors
	if isContextCanceledError(err) {
		return false
	}
	return strings.Contains(msg, "max_token") ||
		strings.Contains(msg, "max tokens") ||
		strings.Contains(msg, "Input is too long") ||
		strings.Contains(msg, "ValidationException") ||
		strings.Contains(msg, "too long")
}

// isThrottlingError checks if an error is due to API throttling
func isThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from throttling errors
	if isContextCanceledError(err) {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "ThrottlingException") ||
		strings.Contains(errStr, "Too many tokens") ||
		strings.Contains(errStr, "StatusCode: 429") ||
		strings.Contains(errStr, "API returned unexpected status code: 429") ||
		strings.Contains(errStr, "status code: 429") ||
		strings.Contains(errStr, "status code 429") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "throttled")
}

// isEmptyContentError checks if an error is due to empty content in response
func isEmptyContentError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "MALFORMED_FUNCTION_CALL") {
		return false
	}
	return strings.Contains(msg, "Choice.Content is empty string") ||
		strings.Contains(msg, "empty content error") ||
		strings.Contains(msg, "choice.Content is empty") ||
		strings.Contains(msg, "empty response")
}

// isZeroCandidatesError checks if an error is due to zero candidates returned
func isZeroCandidatesError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "zero candidates") ||
		strings.Contains(msg, "returned zero candidates") ||
		strings.Contains(msg, "no candidates")
}

// isConnectionError checks if an error is due to connection issues
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from connection errors
	if isContextCanceledError(err) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "network") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection lost") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "unexpected EOF")
}

// isStreamError checks if an error is due to streaming issues
func isStreamError(err error) bool {
	if err == nil {
		return false
	}
	// Exclude context cancellation from stream errors
	if isContextCanceledError(err) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "stream error") ||
		strings.Contains(msg, "stream ID") ||
		strings.Contains(msg, "streaming") ||
		strings.Contains(msg, "stream closed") ||
		strings.Contains(msg, "stream interrupted") ||
		strings.Contains(msg, "stream timeout") ||
		strings.Contains(msg, "streaming error")
}

// isInternalError checks if an error is due to internal server issues
func isInternalError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "INTERNAL_ERROR") ||
		strings.Contains(msg, "internal error") ||
		strings.Contains(msg, "server error") ||
		strings.Contains(msg, "unexpected error") ||
		strings.Contains(msg, "received from peer") ||
		strings.Contains(msg, "peer error") ||
		strings.Contains(msg, "internal server error") ||
		strings.Contains(msg, "service error") ||
		strings.Contains(msg, "status 500") ||
		strings.Contains(msg, "status code: 500") ||
		strings.Contains(msg, "status code 500") ||
		strings.Contains(msg, "StatusCode: 500") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "status 502") ||
		strings.Contains(msg, "status code: 502") ||
		strings.Contains(msg, "status code 502") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "status 503") ||
		strings.Contains(msg, "status code: 503") ||
		strings.Contains(msg, "status code 503") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "status 504") ||
		strings.Contains(msg, "status code: 504") ||
		strings.Contains(msg, "status code 504") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "API returned unexpected status code: 5") ||
		strings.Contains(msg, "Bad Gateway") ||
		strings.Contains(msg, "Service Unavailable") ||
		strings.Contains(msg, "Gateway Timeout")
}

// classifyLLMError categorizes the given error into a known LLM error type
func classifyLLMError(err error) string {
	if isMaxTokenError(err) {
		return "max_token_error"
	} else if isThrottlingError(err) {
		return "throttling_error"
	} else if isZeroCandidatesError(err) {
		return "zero_candidates_error"
	} else if isEmptyContentError(err) {
		return "empty_content_error"
	} else if isConnectionError(err) {
		return "connection_error"
	} else if isStreamError(err) {
		return "stream_error"
	} else if isInternalError(err) {
		return "internal_error"
	}
	return ""
}

// prepareFallbackModels determines the provider and fallback models for the agent
func (a *Agent) prepareFallbackModels() (llm.Provider, []string, []string) {
	v2Logger := a.Logger
	v2Logger.Debug("Getting fallback models", loggerv2.String("provider_field", string(a.provider)))

	// Validate and fallback provider
	var provider llm.Provider
	var err error
	if a.provider != "" {
		provider, err = llm.ValidateProvider(string(a.provider))
		if err != nil {
			v2Logger.Warn("Invalid provider, using default provider 'bedrock'",
				loggerv2.String("provider", string(a.provider)),
				loggerv2.Error(err))
			provider = llm.ProviderBedrock
		}
	} else {
		v2Logger.Debug("No provider specified, using default provider 'bedrock'")
		provider = llm.ProviderBedrock
	}

	// Determine fallback models
	sameProviderFallbacks := llm.GetDefaultFallbackModels(provider)
	var crossProviderFallbacks []string

	if a.CrossProviderFallback != nil {
		crossProviderFallbacks = a.CrossProviderFallback.Models
	} else {
		crossProviderFallbacks = llm.GetCrossProviderFallbackModels(provider)
	}

	return provider, sameProviderFallbacks, crossProviderFallbacks
}

// streamingManager handles streaming state and goroutine management
type streamingManager struct {
	streamChan        chan llmtypes.StreamChunk
	streamingDone     chan bool
	contentChunkIndex int
	totalChunks       int
	startTime         time.Time
}

// startStreaming initializes streaming if enabled and on the first attempt
func (a *Agent) startStreaming(ctx context.Context, attempt int, opts *[]llmtypes.CallOption) *streamingManager {
	if !a.EnableStreaming || attempt != 0 {
		return nil
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 100),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}

	*opts = append(*opts, llmtypes.WithStreamingChan(sm.streamChan))

	a.EmitTypedEvent(ctx, &events.StreamingStartEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		Model:         a.ModelID,
		Provider:      string(a.provider),
	})

	go sm.processChunks(ctx, a)
	return sm
}

// processChunks runs in a goroutine to handle incoming streaming chunks
func (sm *streamingManager) processChunks(ctx context.Context, a *Agent) {
	defer func() {
		sm.streamingDone <- true
	}()

	for chunk := range sm.streamChan {
		if chunk.Type == llmtypes.StreamChunkTypeContent && chunk.Content != "" {
			sm.contentChunkIndex++
			sm.totalChunks++

			a.EmitTypedEvent(ctx, &events.StreamingChunkEvent{
				BaseEventData: events.BaseEventData{Timestamp: time.Now()},
				Content:       chunk.Content,
				ChunkIndex:    sm.contentChunkIndex,
				IsToolCall:    false,
			})

			if a.StreamingCallback != nil {
				a.StreamingCallback(chunk)
			}
		}
	}
}

// finishStreaming waits for streaming to complete and emits the end event
func (a *Agent) finishStreaming(ctx context.Context, sm *streamingManager, resp *llmtypes.ContentResponse) {
	if sm == nil {
		return
	}

	<-sm.streamingDone

	endEvent := &events.StreamingEndEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		TotalChunks:   sm.totalChunks,
		Duration:      time.Since(sm.startTime).String(),
	}

	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		if resp.Choices[0].GenerationInfo.TotalTokens != nil {
			endEvent.TotalTokens = *resp.Choices[0].GenerationInfo.TotalTokens
		}
		if resp.Choices[0].StopReason != "" {
			endEvent.FinishReason = resp.Choices[0].StopReason
		}
	}
	a.EmitTypedEvent(ctx, endEvent)
}

// GenerateContentWithRetry handles LLM generation with robust retry logic for throttling errors
func GenerateContentWithRetry(a *Agent, ctx context.Context, messages []llmtypes.MessageContent, opts []llmtypes.CallOption, turn int) (*llmtypes.ContentResponse, observability.UsageMetrics, error) {
	logger := getLogger(a)
	logger.Info(fmt.Sprintf("🔄 [DEBUG] GenerateContentWithRetry START - Messages: %d, Options: %d, Turn: %d", len(messages), len(opts), turn))

	maxRetries := 5
	baseDelay := 30 * time.Second
	maxDelay := 5 * time.Minute
	var lastErr error
	var usage observability.UsageMetrics

	provider, sameProviderFallbacks, crossProviderFallbacks := a.prepareFallbackModels()
	
	generationStartTime := time.Now()

	// Emit start event
	a.EmitTypedEvent(ctx, &events.LLMGenerationWithRetryEvent{
		BaseEventData:          events.BaseEventData{Timestamp: generationStartTime},
		Turn:                   turn,
		MaxRetries:             maxRetries,
		PrimaryModel:           a.ModelID,
		CurrentLLM:             a.ModelID,
		SameProviderFallbacks:  sameProviderFallbacks,
		CrossProviderFallbacks: crossProviderFallbacks,
		Provider:               string(provider),
		Operation:              "llm_generation_with_fallback",
		Status:                 "started",
	})

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, usage, a.handleContextCancellation(ctx, turn, generationStartTime)
		}
		
		// Create a copy of options for this attempt to avoid polluting the original slice
		// especially when appending streaming channel
		currentOpts := make([]llmtypes.CallOption, len(opts))
		copy(currentOpts, opts)

		sm := a.startStreaming(ctx, attempt, &currentOpts)

		resp, err := a.LLM.GenerateContent(ctx, messages, currentOpts...)
		
		a.finishStreaming(ctx, sm, resp)

		if err == nil {
			usage = extractUsageMetricsWithMessages(resp, messages)
			return resp, usage, nil
		}
		
		// Handle context cancellation specifically
		if isContextCanceledError(err) || ctx.Err() != nil {
			return nil, usage, a.handleContextCancellation(ctx, turn, generationStartTime)
		}
		
		// Emit error event for actual errors (not cancellations)
		a.EmitTypedEvent(ctx, &events.LLMGenerationErrorEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			Turn:          turn + 1,
			ModelID:       a.ModelID,
			Error:         err.Error(),
			Duration:      time.Since(generationStartTime),
		})

		errorType := classifyLLMError(err)
		if errorType == "" {
			lastErr = err
			break
		}

		// Special handling for retrying original model
		if (errorType == "throttling_error" || errorType == "zero_candidates_error") && attempt < maxRetries-1 {
			shouldRetry, _, retryErr := retryOriginalModel(a, ctx, errorType, attempt, maxRetries, baseDelay, maxDelay, turn, logger, usage)
			if retryErr != nil {
				return nil, usage, retryErr
			}
			if shouldRetry {
				continue
			}
		}

		fResp, fUsage, fErr := handleErrorWithFallback(a, ctx, err, errorType, turn, attempt, maxRetries, sameProviderFallbacks, crossProviderFallbacks, messages, opts)
		if fErr == nil {
			return fResp, fUsage, nil
		}
		lastErr = fErr
		break
	}

	return nil, usage, lastErr
}

// handleContextCancellation emits cancellation event and returns the error
func (a *Agent) handleContextCancellation(ctx context.Context, turn int, startTime time.Time) error {
	err := ctx.Err()
	if err == nil {
		err = context.Canceled
	}
	a.EmitTypedEvent(ctx, events.NewContextCancelledEvent(turn, err.Error(), time.Since(startTime)))
	return err
}

// handleErrorWithFallback is a generic function that handles any error type with fallback models
func handleErrorWithFallback(a *Agent, ctx context.Context, err error, errorType string, turn int, attempt int, maxRetries int, sameProviderFallbacks, crossProviderFallbacks []string, messages []llmtypes.MessageContent, opts []llmtypes.CallOption) (*llmtypes.ContentResponse, observability.UsageMetrics, error) {
	// 🔧 FIX: Reset reasoning tracker to prevent infinite final answer events

	// Check if context is canceled - we should not do fallback if context is canceled
	if ctx.Err() != nil {
		return nil, observability.UsageMetrics{}, fmt.Errorf("context canceled: we should not do fallback: %w", ctx.Err())
	}

	// Also check if the error itself is a context cancellation
	if isContextCanceledError(err) {
		return nil, observability.UsageMetrics{}, fmt.Errorf("context canceled: we should not do fallback: %w", err)
	}

	// Track error start time
	errorStartTime := time.Now()

	// Emit error detected event
	errorEvent := events.NewThrottlingDetectedEvent(turn, a.ModelID, string(a.provider), attempt+1, maxRetries, time.Since(errorStartTime), errorType, 0)
	a.EmitTypedEvent(ctx, errorEvent)

	// Create error fallback event
	errorFallbackEvent := &events.GenericEventData{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Data: map[string]interface{}{
			"error_type":               errorType,
			"original_error":           err.Error(),
			"same_provider_fallbacks":  len(sameProviderFallbacks),
			"cross_provider_fallbacks": len(crossProviderFallbacks),
			"turn":                     turn,
			"attempt":                  attempt + 1,
			"operation":                errorType + "_fallback",
		},
	}
	a.EmitTypedEvent(ctx, errorFallbackEvent)

	// Phase 1: Try same-provider fallbacks first
	for i, fallbackModelID := range sameProviderFallbacks {

		origModelID := a.ModelID
		a.ModelID = fallbackModelID
		fallbackLLM, ferr := a.createFallbackLLM(ctx, fallbackModelID)
		if ferr != nil {
			a.ModelID = origModelID
			continue
		}

		origLLM := a.LLM
		a.LLM = fallbackLLM

		// Use non-streaming approach for all agents during fallback
		fresp, ferr2 := a.LLM.GenerateContent(ctx, messages, opts...)

		a.LLM = origLLM
		a.ModelID = origModelID

		if ferr2 == nil {
			usage := extractUsageMetricsWithMessages(fresp, messages)

			// Detect the actual provider for the fallback model
			fallbackProvider := detectProviderFromModelID(fallbackModelID)

			// Emit fallback attempt event for successful attempt
			fallbackAttemptEvent := events.NewFallbackAttemptEvent(
				turn, i+1, len(sameProviderFallbacks),
				fallbackModelID, string(fallbackProvider), "same_provider",
				true, time.Since(errorStartTime), "",
			)
			a.EmitTypedEvent(ctx, fallbackAttemptEvent)

			// Emit fallback model used event
			fallbackEvent := events.NewFallbackModelUsedEvent(turn, origModelID, fallbackModelID, string(fallbackProvider), errorType, time.Since(errorStartTime))
			a.EmitTypedEvent(ctx, fallbackEvent)

			// Emit model change event to track the permanent model change
			modelChangeEvent := events.NewModelChangeEvent(turn, origModelID, fallbackModelID, "fallback_success", string(fallbackProvider), time.Since(errorStartTime))
			a.EmitTypedEvent(ctx, modelChangeEvent)

			// PERMANENTLY UPDATE AGENT'S MODEL to the successful fallback
			a.ModelID = fallbackModelID
			a.LLM = fallbackLLM

			return fresp, usage, nil
		} else {
			// Emit fallback attempt event for generation failure
			failureEvent := events.NewFallbackAttemptEvent(
				turn, i+1, len(sameProviderFallbacks),
				fallbackModelID, string(detectProviderFromModelID(fallbackModelID)), "same_provider",
				false, time.Since(errorStartTime), ferr2.Error(),
			)
			a.EmitTypedEvent(ctx, failureEvent)
		}
	}

	// Phase 2: Try cross-provider fallbacks if same-provider fallbacks failed
	if len(crossProviderFallbacks) > 0 {
		for i, fallbackModelID := range crossProviderFallbacks {
			origModelID := a.ModelID
			a.ModelID = fallbackModelID
			fallbackLLM, ferr := a.createFallbackLLM(ctx, fallbackModelID)
			if ferr != nil {
				a.ModelID = origModelID
				continue
			}

			origLLM := a.LLM
			a.LLM = fallbackLLM

			// Use non-streaming approach for all agents during fallback
			fresp, ferr2 := a.LLM.GenerateContent(ctx, messages, opts...)

			a.LLM = origLLM
			a.ModelID = origModelID

			if ferr2 == nil {
				usage := extractUsageMetricsWithMessages(fresp, messages)

				// Detect the actual provider for the fallback model
				fallbackProvider := detectProviderFromModelID(fallbackModelID)

				// Emit fallback attempt event for successful attempt
				fallbackAttemptEvent := events.NewFallbackAttemptEvent(
					turn, i+1, len(crossProviderFallbacks),
					fallbackModelID, string(fallbackProvider), "cross_provider",
					true, time.Since(errorStartTime), "",
				)
				a.EmitTypedEvent(ctx, fallbackAttemptEvent)

				// Emit fallback model used event
				fallbackEvent := events.NewFallbackModelUsedEvent(turn, origModelID, fallbackModelID, string(fallbackProvider), errorType, time.Since(errorStartTime))
				a.EmitTypedEvent(ctx, fallbackEvent)

				// Emit model change event to track the permanent model change
				modelChangeEvent := events.NewModelChangeEvent(turn, origModelID, fallbackModelID, "fallback_success", string(fallbackProvider), time.Since(errorStartTime))
				a.EmitTypedEvent(ctx, modelChangeEvent)

				// PERMANENTLY UPDATE AGENT'S MODEL to the successful fallback
				a.ModelID = fallbackModelID
				a.LLM = fallbackLLM

				return fresp, usage, nil
			} else {
				// Emit fallback attempt event for generation failure
				failureEvent := events.NewFallbackAttemptEvent(
					turn, i+1, len(crossProviderFallbacks),
					fallbackModelID, string(detectProviderFromModelID(fallbackModelID)), "cross_provider",
					false, time.Since(errorStartTime), ferr2.Error(),
				)
				a.EmitTypedEvent(ctx, failureEvent)
			}
		}
	}

	// If all fallback models failed, emit failure event
	errorAllFailedEvent := &events.GenericEventData{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Data: map[string]interface{}{
			"error_type":  errorType,
			"operation":   errorType + "_fallback",
			"duration":    time.Since(errorStartTime).String(),
			"final_error": err.Error(),
		},
	}
	a.EmitTypedEvent(ctx, errorAllFailedEvent)

	return nil, observability.UsageMetrics{}, fmt.Errorf("all fallback models failed for %s: %w", errorType, err)
}

// createFallbackLLM creates a fallback LLM instance for the given modelID
// ctx is used for cancellation/timeout during initialization
func (a *Agent) createFallbackLLM(ctx context.Context, modelID string) (llmtypes.Model, error) {
	// ✅ FIXED: Detect provider from model ID instead of using agent's provider
	provider := detectProviderFromModelID(modelID)

	// Log the fallback attempt with the detected provider
	v2Logger := a.Logger
	v2Logger.Debug("Creating fallback LLM using detected provider",
		loggerv2.String("model_id", modelID),
		loggerv2.String("detected_provider", string(provider)))

	// Use InitializeLLM from providers.go for all providers for consistency
	// This ensures proper initialization, logging, and event emission
	// Use agent's temperature if available, otherwise default to 0.7
	temperature := a.Temperature
	if temperature == 0 {
		temperature = 0.7
	}

	// Convert Agent API keys to llm ProviderAPIKeys format
	var llmAPIKeys *llm.ProviderAPIKeys
	if a.APIKeys != nil {
		llmAPIKeys = &llm.ProviderAPIKeys{
			OpenRouter: a.APIKeys.OpenRouter,
			OpenAI:     a.APIKeys.OpenAI,
			Anthropic:  a.APIKeys.Anthropic,
			Vertex:     a.APIKeys.Vertex,
		}
		if a.APIKeys.Bedrock != nil {
			llmAPIKeys.Bedrock = &llm.BedrockConfig{
				Region: a.APIKeys.Bedrock.Region,
			}
		}
		v2Logger.Debug("Using API keys from agent config for fallback LLM")
	} else {
		v2Logger.Warn("No API keys in agent config, fallback LLM will use environment variables")
	}

	llmConfig := llm.Config{
		Provider:    provider,
		ModelID:     modelID,
		Temperature: temperature,
		Tracers:     a.Tracers,
		TraceID:     a.TraceID,
		Logger:      a.Logger, // Use agent's v2.Logger (llm.Config expects v2.Logger)
		Context:     ctx,
		APIKeys:     llmAPIKeys,
	}

	llmModel, err := llm.InitializeLLM(llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create fallback LLM for provider %s, model %s: %w", provider, modelID, err)
	}
	return llmModel, nil
}

// detectProviderFromModelID detects the provider based on the model ID
func detectProviderFromModelID(modelID string) llm.Provider {
	// OpenAI models: gpt-*, gpt-4*, gpt-3*, o3*, o4*
	if strings.HasPrefix(modelID, "gpt-") || strings.HasPrefix(modelID, "o3") || strings.HasPrefix(modelID, "o4") {
		return llm.ProviderOpenAI
	}

	// Bedrock models: us.anthropic.* (Bedrock-specific prefix)
	if strings.HasPrefix(modelID, "us.anthropic.") {
		return llm.ProviderBedrock
	}

	// Anthropic models: claude-* (for direct API, not Bedrock)
	if strings.HasPrefix(modelID, "claude-") {
		return llm.ProviderAnthropic
	}

	// Vertex/Gemini models: gemini-* (Google Vertex AI)
	if strings.HasPrefix(modelID, "gemini-") {
		return llm.ProviderVertex
	}

	// OpenRouter models: various model names with "/" separator
	if strings.Contains(modelID, "/") {
		return llm.ProviderOpenRouter
	}

	// Default to Bedrock for unknown models (conservative approach)
	return llm.ProviderBedrock
}
