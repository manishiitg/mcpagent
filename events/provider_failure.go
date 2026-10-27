package events

import (
	"errors"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// SetProviderFailureDisplay keeps failed-turn policy on the server. Metadata is
// also carried by stored/resumed completion events, not just the live banner.
func (e *UnifiedCompletionEvent) SetProviderFailureDisplay() {
	if e.Error == "" {
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
