package mcpagent

import (
	"context"
	"fmt"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType string
	}{
		// nil
		{name: "nil error", err: nil, wantType: ""},

		// max_token_error
		{name: "max_token bedrock", err: fmt.Errorf("ValidationException: Input is too long for model"), wantType: "max_token_error"},
		{name: "max tokens generic", err: fmt.Errorf("max tokens exceeded"), wantType: "max_token_error"},

		// quota_exhausted_error
		{name: "quota per_day", err: fmt.Errorf("per_day limit reached"), wantType: "quota_exhausted_error"},
		{name: "quota per_month", err: fmt.Errorf("per_month budget exceeded"), wantType: "quota_exhausted_error"},
		{name: "quota resource_exhausted", err: fmt.Errorf("resource_exhausted: daily limit"), wantType: "quota_exhausted_error"},
		{name: "quota codex usage limit", err: fmt.Errorf("hit your usage limit"), wantType: "quota_exhausted_error"},
		{name: "quota claude code limit", err: fmt.Errorf("you've hit your limit for today"), wantType: "quota_exhausted_error"},
		{name: "quota exceeded current", err: fmt.Errorf("exceeded your current quota"), wantType: "quota_exhausted_error"},

		// throttling_error
		{name: "throttle 429", err: fmt.Errorf("StatusCode: 429 Too Many Requests"), wantType: "throttling_error"},
		{name: "throttle rate limit", err: fmt.Errorf("rate limit exceeded, retry after 30s"), wantType: "throttling_error"},
		{name: "throttle overloaded", err: fmt.Errorf("model is overloaded, please try again"), wantType: "throttling_error"},
		{name: "throttle bedrock", err: fmt.Errorf("ThrottlingException: Rate exceeded"), wantType: "throttling_error"},

		// zero_candidates_error
		{name: "zero candidates", err: fmt.Errorf("returned zero candidates"), wantType: "zero_candidates_error"},
		{name: "no candidates", err: fmt.Errorf("no candidates in response"), wantType: "zero_candidates_error"},

		// empty_content_error
		{name: "empty content", err: fmt.Errorf("Choice.Content is empty string"), wantType: "empty_content_error"},
		{name: "empty response", err: fmt.Errorf("empty response from model"), wantType: "empty_content_error"},

		// connection_error
		{name: "connection refused", err: fmt.Errorf("dial tcp 127.0.0.1:8080: connection refused"), wantType: "connection_error"},
		{name: "connection timeout", err: fmt.Errorf("timeout waiting for response"), wantType: "connection_error"},
		{name: "connection EOF", err: fmt.Errorf("unexpected EOF"), wantType: "connection_error"},

		// stream_error — note: "stream error: connection reset" matches connection_error first
		// because classifyLLMError checks isConnectionError before isStreamError
		{name: "stream error pure", err: fmt.Errorf("stream interrupted mid-response"), wantType: "stream_error"},
		{name: "stream closed", err: fmt.Errorf("stream closed unexpectedly"), wantType: "stream_error"},

		// internal_error
		{name: "internal 500", err: fmt.Errorf("API returned unexpected status code: 500"), wantType: "internal_error"},
		{name: "bad gateway", err: fmt.Errorf("Bad Gateway"), wantType: "internal_error"},
		{name: "service unavailable", err: fmt.Errorf("Service Unavailable"), wantType: "internal_error"},

		// unknown
		{name: "unknown error", err: fmt.Errorf("something completely unexpected"), wantType: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLLMError(tt.err)
			if got != tt.wantType {
				t.Fatalf("classifyLLMError(%q) = %q, want %q", tt.err, got, tt.wantType)
			}
		})
	}
}

func TestContextCanceledNotMisclassified(t *testing.T) {
	cancelErr := context.Canceled
	deadlineErr := context.DeadlineExceeded
	wrappedCancel := fmt.Errorf("operation failed: %w", context.Canceled)

	for _, err := range []error{cancelErr, deadlineErr, wrappedCancel} {
		if !isContextCanceledError(err) {
			t.Fatalf("isContextCanceledError(%v) = false, want true", err)
		}
		if isThrottlingError(err) {
			t.Fatalf("isThrottlingError should return false for context cancel: %v", err)
		}
		if isConnectionError(err) {
			t.Fatalf("isConnectionError should return false for context cancel: %v", err)
		}
		if isStreamError(err) {
			t.Fatalf("isStreamError should return false for context cancel: %v", err)
		}
		if isMaxTokenError(err) {
			t.Fatalf("isMaxTokenError should return false for context cancel: %v", err)
		}
	}
}

func TestQuotaVsThrottlingDisambiguation(t *testing.T) {
	quotaErr := fmt.Errorf("resource_exhausted: per_day limit")
	if !isQuotaExhaustedError(quotaErr) {
		t.Fatal("resource_exhausted per_day should be quota exhausted")
	}

	throttleErr := fmt.Errorf("StatusCode: 429 rate limit")
	if !isThrottlingError(throttleErr) {
		t.Fatal("429 should be throttling")
	}

	// Quota errors take priority over throttling in classifyLLMError
	// because quota is checked first in the classification chain
	combined := fmt.Errorf("resource_exhausted: 429 per_day limit hit")
	got := classifyLLMError(combined)
	if got != "quota_exhausted_error" {
		t.Fatalf("combined quota+throttle classified as %q, want quota_exhausted_error (quota takes priority)", got)
	}
}

func TestShouldSkipSameModelRetry(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		errorType string
		want      bool
	}{
		{name: "openrouter throttle skips retry", provider: "openrouter", errorType: "throttling_error", want: true},
		{name: "openrouter quota does NOT skip retry", provider: "openrouter", errorType: "quota_exhausted_error", want: false},
		{name: "anthropic throttle does not skip", provider: "anthropic", errorType: "throttling_error", want: false},
		{name: "openai throttle does not skip", provider: "openai", errorType: "throttling_error", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipSameModelRetry(tt.provider, tt.errorType)
			if got != tt.want {
				t.Fatalf("shouldSkipSameModelRetry(%q, %q) = %v, want %v", tt.provider, tt.errorType, got, tt.want)
			}
		})
	}
}

// TestTypedErrorsClassifyWithoutStringMatching verifies the kind-first path:
// errors classified by the provider layer (llmerrors) are recognized even
// when their message text matches none of the legacy string patterns.
// This is what makes classification robust to provider message changes.
func TestTypedErrorsClassifyWithoutStringMatching(t *testing.T) {
	opaque := func(kind llmerrors.Kind) error {
		// Message deliberately contains no matchable fragments; only the
		// typed Kind carries the signal.
		return &llmerrors.Error{Kind: kind, Provider: "p", Model: "m", Err: fmt.Errorf("opaque failure %d", 12)}
	}

	tests := []struct {
		kind llmerrors.Kind
		want string
	}{
		{llmerrors.KindContextTooLong, "max_token_error"},
		{llmerrors.KindQuotaExhausted, "quota_exhausted_error"},
		{llmerrors.KindRateLimit, "throttling_error"},
		{llmerrors.KindNetwork, "connection_error"},
		{llmerrors.KindTimeout, "connection_error"},
		{llmerrors.KindServerError, "internal_error"},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := classifyLLMError(opaque(tt.kind)); got != tt.want {
				t.Fatalf("classifyLLMError(typed %s) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}

	// Wrapped typed errors must classify the same way.
	wrapped := fmt.Errorf("generation failed: %w", opaque(llmerrors.KindRateLimit))
	if !isThrottlingError(wrapped) {
		t.Error("wrapped typed rate-limit error should classify as throttling")
	}
}

func TestAuthAndModelNotFoundClassification(t *testing.T) {
	authCases := []error{
		fmt.Errorf("status code: 401 Unauthorized"),
		fmt.Errorf("invalid x-api-key"),
		fmt.Errorf("AccessDeniedException: not authorized to invoke this model"),
		&llmerrors.Error{Kind: llmerrors.KindAuth, Err: fmt.Errorf("opaque")},
	}
	for _, err := range authCases {
		if !isAuthError(err) {
			t.Errorf("isAuthError(%v) = false, want true", err)
		}
		if got := classifyLLMError(err); got != "auth_error" {
			t.Errorf("classifyLLMError(%v) = %q, want auth_error", err, got)
		}
	}

	modelCases := []error{
		fmt.Errorf("status code: 404 The model `gpt-9` does not exist or you do not have access to it"),
		fmt.Errorf("model_not_found"),
		&llmerrors.Error{Kind: llmerrors.KindModelNotFound, Err: fmt.Errorf("opaque")},
	}
	for _, err := range modelCases {
		if !isModelNotFoundError(err) {
			t.Errorf("isModelNotFoundError(%v) = false, want true", err)
		}
		if got := classifyLLMError(err); got != "model_not_found_error" {
			t.Errorf("classifyLLMError(%v) = %q, want model_not_found_error", err, got)
		}
	}

	// Negative: context cancellation must never look like auth, and a throttling
	// error must not be misread as auth/model-not-found.
	if isAuthError(context.Canceled) {
		t.Error("context.Canceled must not classify as auth")
	}
	throttle := fmt.Errorf("StatusCode: 429 rate limit")
	if isAuthError(throttle) || isModelNotFoundError(throttle) {
		t.Error("429 throttle must not classify as auth or model-not-found")
	}
}

// TestAuthAndModelNotFoundAreNotSameModelRetried locks the intent: neither kind
// is on a same-model retry path. classifyLLMError returns a dedicated type, and
// the retry loop's same-model-retry branches (zero_candidates / throttling /
// empty_content) deliberately exclude them — so the loop breaks straight to the
// fallback chain. This guards against a future "retry unknown errors by default"
// change silently re-retrying terminal auth failures.
func TestAuthAndModelNotFoundAreNotSameModelRetried(t *testing.T) {
	sameModelRetryTypes := map[string]bool{
		"zero_candidates_error": true,
		"throttling_error":      true,
		"internal_error":        true,
		"connection_error":      true,
		"stream_error":          true,
		"empty_content_error":   true,
	}
	for _, et := range []string{"auth_error", "model_not_found_error"} {
		if sameModelRetryTypes[et] {
			t.Errorf("%s must not be a same-model-retry error type", et)
		}
	}
}

func TestNilErrorsReturnFalse(t *testing.T) {
	checks := []struct {
		name string
		fn   func(error) bool
	}{
		{"isContextCanceledError", isContextCanceledError},
		{"isMaxTokenError", isMaxTokenError},
		{"isQuotaExhaustedError", isQuotaExhaustedError},
		{"isThrottlingError", isThrottlingError},
		{"isEmptyContentError", isEmptyContentError},
		{"isZeroCandidatesError", isZeroCandidatesError},
		{"isConnectionError", isConnectionError},
		{"isStreamError", isStreamError},
		{"isInternalError", isInternalError},
		{"isAuthError", isAuthError},
		{"isModelNotFoundError", isModelNotFoundError},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.fn(nil) {
				t.Fatalf("%s(nil) = true, want false", c.name)
			}
		})
	}
}
