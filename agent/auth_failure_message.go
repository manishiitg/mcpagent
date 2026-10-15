package mcpagent

import (
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// invalidAPIKeyPhrases are the provider texts for a rejected key. A permission error without one of them keeps its
// own text.
var invalidAPIKeyPhrases = []string{"incorrect api key provided", "invalid_api_key", "invalid api key", "invalid x-api-key", "api key not valid"}

// plainAuthFailureMessage turns a provider's rejection of the API key into words a person can act on, and says so in the
// log (PLAT-861-220). It returns "" for any other error, which then shows as before.
func plainAuthFailureMessage(provider string, err error) string {
	if err == nil || llmerrors.KindOf(err) != llmerrors.KindAuth {
		return ""
	}
	text := strings.ToLower(err.Error())
	for _, phrase := range invalidAPIKeyPhrases {
		if strings.Contains(text, phrase) {
			log.Printf("[TURN] stalled cause=invalid_api_key provider=%s", provider)
			return providerLabel(provider) + " rejected the API key on this account. Update the key in Accounts, or pick another agent. Your message is saved."
		}
	}
	return ""
}

func providerLabel(provider string) string {
	switch provider {
	case "codex-cli":
		return "OpenAI (Codex)"
	case "claude-code":
		return "Anthropic (Claude)"
	case "cursor-cli":
		return "Cursor"
	case "muse-cli":
		return "Muse"
	case "agy-cli":
		return "Antigravity"
	case "pi-cli":
		return "Pi"
	}
	if provider == "" {
		return "The provider"
	}
	return provider
}
