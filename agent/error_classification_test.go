package mcpagent

import (
	"context"
	"fmt"
	"testing"
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
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.fn(nil) {
				t.Fatalf("%s(nil) = true, want false", c.name)
			}
		})
	}
}
