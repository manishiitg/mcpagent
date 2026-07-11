package mcpagent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
)

func cleanupInactiveCodingAgentProjectArtifacts(workingDir string, activeProvider llm.Provider) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return
	}
	active := string(activeProvider)

	if active != string(llm.ProviderClaudeCode) {
		removeManagedInstructionFile(filepath.Join(workingDir, "CLAUDE.md"))
		removeManagedDir(filepath.Join(workingDir, ".claude"))
	}
	if active != string(llm.ProviderCodexCLI) {
		removeManagedInstructionFile(filepath.Join(workingDir, "AGENTS.md"))
		removeManagedDir(filepath.Join(workingDir, ".codex"))
	}
	removeManagedInstructionFile(filepath.Join(workingDir, "GEMINI.md"))
	removeManagedDir(filepath.Join(workingDir, ".gemini"))
	removeManagedDir(filepath.Join(workingDir, ".gemini-main"))
	if active != string(llm.ProviderCursorCLI) {
		removeManagedDir(filepath.Join(workingDir, ".cursor"))
	}
	if active != string(llm.ProviderPiCLI) {
		removeManagedDir(filepath.Join(workingDir, ".pi"))
	}
	if active != string(llm.ProviderAgyCLI) && active != string(llm.ProviderCodexCLI) {
		removeManagedDir(filepath.Join(workingDir, ".agents"))
	}
	if active != string(llm.ProviderAgyCLI) {
		removeManagedFile(filepath.Join(workingDir, ".agents", "rules", "mlp-system.md"))
		removeManagedFileIfGenerated(filepath.Join(workingDir, ".agents", "mcp_config.json"))
		removeManagedFileIfGenerated(filepath.Join(workingDir, ".agents", "hooks.json"))
		removeManagedFile(filepath.Join(workingDir, ".agents", "mlp-bridge-only-hook.sh"))
		removeManagedFile(filepath.Join(workingDir, ".agents", "mlp-bridge-only-denials.jsonl"))
	}
	pruneEmptyDirs(
		filepath.Join(workingDir, ".agents", "rules"),
		filepath.Join(workingDir, ".agents", "skills"),
		filepath.Join(workingDir, ".agents"),
	)
}

func removeManagedInstructionFile(path string) {
	// #nosec G304 - path is built from the configured coding-agent working directory.
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if strings.Contains(string(body), "mlp-session-instructions") {
		_ = os.Remove(path)
	}
}

func removeManagedFileIfGenerated(path string) {
	// #nosec G304 - path is built from the configured coding-agent working directory.
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(body)
	if strings.Contains(text, "api-bridge") ||
		strings.Contains(text, "MCP_TOOLS") ||
		strings.Contains(text, "mcpbridge") ||
		strings.Contains(text, "mlp-deny-builtin") ||
		strings.Contains(text, "mlp-bridge-only") {
		_ = os.Remove(path)
	}
}

func removeManagedFile(path string) {
	_ = os.Remove(path)
}

func removeManagedDir(path string) {
	_ = os.RemoveAll(path)
}

func pruneEmptyDirs(paths ...string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}
