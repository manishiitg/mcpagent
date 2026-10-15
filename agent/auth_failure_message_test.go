package mcpagent

import (
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// Production 2026-10-15: a Codex turn failed with OpenAI's 401 and the chat showed the raw provider text.
func TestPlainAuthFailureMessage(t *testing.T) {
	raw := errors.New("codex-cli turn failed: unexpected status 401 Unauthorized: Incorrect API key provided: sk-proj-**********")
	typed := &llmerrors.Error{Kind: llmerrors.KindAuth, Provider: "codex-cli", Err: raw}
	got := plainAuthFailureMessage("codex-cli", typed)
	for _, want := range []string{"OpenAI (Codex) rejected the API key", "Update the key in Accounts", "pick another agent", "message is saved"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "sk-proj") {
		t.Fatalf("no key text in the plain message: %q", got)
	}
	// An auth error that is not a rejected key keeps its own text, as does any other kind of error.
	denied := &llmerrors.Error{Kind: llmerrors.KindAuth, Provider: "codex-cli", Err: errors.New("403 permission denied for this model")}
	if got := plainAuthFailureMessage("codex-cli", denied); got != "" {
		t.Fatalf("a permission error must keep its own text, got %q", got)
	}
	other := &llmerrors.Error{Kind: llmerrors.KindRateLimit, Provider: "codex-cli", Err: errors.New("Incorrect API key provided")}
	if got := plainAuthFailureMessage("codex-cli", other); got != "" {
		t.Fatalf("only auth failures are rewritten, got %q", got)
	}
	if plainAuthFailureMessage("codex-cli", nil) != "" {
		t.Fatal("nil error")
	}
}
