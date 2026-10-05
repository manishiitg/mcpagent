package mcpagent

import (
	"strings"
	"testing"
)

// PLAT-519: aliases are short and never the internal key; two connections of
// one server get stable distinct aliases; any name stays valid and unique
// within the 64-character provider limit.
func TestMCPToolAliasesAndVisibleNames(t *testing.T) {
	aliases := mcpToolAliases([]string{
		"ue03d5aec7071d9d6e888aca57a337a56__neon_46fb2cad6010",
		"ue03d5aec7071d9d6e888aca57a337a56__neon_9c1d00aa7713",
		"google-sheets",
	})
	for server, want := range map[string]string{
		"ue03d5aec7071d9d6e888aca57a337a56__neon_46fb2cad6010": "neon_46fb",
		"ue03d5aec7071d9d6e888aca57a337a56__neon_9c1d00aa7713": "neon_9c1d",
		"google-sheets": "google-sheets",
	} {
		if aliases[server] != want {
			t.Errorf("alias(%s) = %q, want %q", server, aliases[server], want)
		}
	}

	long := strings.Repeat("very_long_tool_name_", 5)
	first := mcpVisibleToolName("neon_46fb", "s", long+"a")
	second := mcpVisibleToolName("neon_46fb", "s", long+"b")
	odd := mcpVisibleToolName("neon_46fb", "s", "notion.search page")
	for _, name := range []string{first, second, odd} {
		if !isValidToolName(name) || !strings.HasPrefix(name, "neon_46fb__") {
			t.Errorf("visible name %q is not a valid prefixed tool name", name)
		}
	}
	if first == second {
		t.Errorf("two long tool names collapsed to %q", first)
	}
}
