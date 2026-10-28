package events

import (
	"errors"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// SetProviderFailureDisplay keeps failed-turn policy on the server. Metadata is
// also carried by stored/resumed completion events, not just the live banner.
func (e *UnifiedCompletionEvent) SetProviderFailureDisplay() {
	// A turn the person stopped is not a provider failure (server B 2026-10-28: Stop showed "The provider could
	// not complete this response").
	if e.Error == "" || strings.Contains(e.Error, "context canceled") {
		return
	}
	d := llmerrors.ProviderDisplay(errors.New(e.Error))
	if e.Metadata == nil {
		e.Metadata = make(map[string]interface{})
	}
	e.Metadata["error_code"] = d.Code
	e.Metadata["provider_message"] = d.Message
	e.Metadata["next_step"] = d.NextStep
	e.Metadata["retryable"] = d.Retryable
}
