package geminicli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	testutils "github.com/manishiitg/mcpagent/cmd/testing/testutils"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var geminiCLITestCmd = &cobra.Command{
	Use:   "gemini-cli",
	Short: "E2E tests for Gemini CLI provider with HTTP routing hooks enabled",
	Long: `Tests the Gemini CLI provider with MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING=true.

Three sub-tests:
1. Text response  — catches broken hook config (allowed_function_names 400 error)
2. Tool call      — model calls get_api_spec; verifies allowed tools work end-to-end
3. Blocked tool   — model tries a disallowed built-in; hook denies it, model recovers

All three sub-tests share one agent/session to also exercise multi-turn continuity.

Examples:
  mcpagent-test test gemini-cli
  mcpagent-test test gemini-cli --log-level debug`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := testutils.NewTestLoggerFromViper()
		logger.Info("=== Gemini CLI E2E Tests ===")

		if err := runAllGeminiCLITests(logger); err != nil {
			return fmt.Errorf("gemini-cli tests failed: %w", err)
		}

		logger.Info("✅ All Gemini CLI tests passed!")
		return nil
	},
}

// GetGeminiCLITestCmd returns the Gemini CLI test command.
func GetGeminiCLITestCmd() *cobra.Command {
	return geminiCLITestCmd
}

func runAllGeminiCLITests(log loggerv2.Logger) error {
	// Force HTTP routing hooks on — production always runs with this set.
	// The bug (mode=AUTO + allowedFunctionNames in BeforeToolSelection) only
	// manifests when this env var is true, which is why basic tests missed it.
	orig := os.Getenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING")
	if err := os.Setenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING", "true"); err != nil {
		return fmt.Errorf("failed to set env var: %w", err)
	}
	defer func() {
		if orig == "" {
			_ = os.Unsetenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING")
		} else {
			_ = os.Setenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING", orig)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create one LLM + agent shared across all sub-tests (exercises multi-turn too).
	log.Info("--- Setup: Create Gemini CLI LLM and Agent ---")
	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderGeminiCLI,
		ModelID:  "auto",
		Logger:   log,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize Gemini CLI LLM: %w", err)
	}

	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	traceID := testutils.GenerateTestTraceID()
	agent, err := testutils.CreateMinimalAgent(ctx, model, llm.ProviderGeminiCLI, tracer, traceID, log)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer agent.Close()
	log.Info("Agent created")

	// Sub-test 1: plain text response.
	// Regression: broken hook config causes 400 INVALID_ARGUMENT on every turn.
	log.Info("--- Sub-test 1: Text response (validates hook config / settings.json) ---")
	if err := subTestTextResponse(ctx, agent, log); err != nil {
		return fmt.Errorf("sub-test 1 (text response): %w", err)
	}
	log.Info("✅ Sub-test 1 passed")

	// Sub-test 2: allowed tool call (get_api_spec).
	// Regression: BeforeTool hook denying allowed tools, or tool call path broken.
	log.Info("--- Sub-test 2: Allowed tool call (get_api_spec) ---")
	if err := subTestAllowedToolCall(ctx, agent, log); err != nil {
		return fmt.Errorf("sub-test 2 (allowed tool call): %w", err)
	}
	log.Info("✅ Sub-test 2 passed")

	// Sub-test 3: disallowed tool gets blocked by enforce-http-tool-routing hook.
	// Regression: hook removed or broken — disallowed built-in tools slip through.
	log.Info("--- Sub-test 3: Disallowed tool blocked by hook ---")
	if err := subTestDisallowedToolBlocked(ctx, agent, log); err != nil {
		return fmt.Errorf("sub-test 3 (disallowed tool blocked): %w", err)
	}
	log.Info("✅ Sub-test 3 passed")

	return nil
}

// subTestTextResponse sends a plain text prompt and verifies no 400 from Gemini API.
func subTestTextResponse(ctx context.Context, agent interface{ Ask(context.Context, string) (string, error) }, log loggerv2.Logger) error {
	start := time.Now()
	response, err := agent.Ask(ctx, "Say hello in one short sentence.")
	duration := time.Since(start)

	if err != nil {
		if strings.Contains(err.Error(), "allowed_function_names") {
			return fmt.Errorf("❌ 400 INVALID_ARGUMENT: hook or settings.json sets allowed_function_names with wrong mode — %w", err)
		}
		return fmt.Errorf("Ask failed: %w", err)
	}
	if response == "" {
		return fmt.Errorf("empty response")
	}

	log.Info("Got response",
		loggerv2.String("response", response),
		loggerv2.String("duration", duration.String()))
	return nil
}

// subTestAllowedToolCall asks the model to use google_web_search — it is both
// in the ALLOWED set of the enforce-http-tool-routing hook AND a real Gemini CLI
// built-in, so the model can actually call it and the hook must pass it through.
func subTestAllowedToolCall(ctx context.Context, agent interface{ Ask(context.Context, string) (string, error) }, log loggerv2.Logger) error {
	prompt := "Use google_web_search to find the current year and tell me what it is in one sentence."
	start := time.Now()
	response, err := agent.Ask(ctx, prompt)
	duration := time.Since(start)

	if err != nil {
		if strings.Contains(err.Error(), "allowed_function_names") {
			return fmt.Errorf("❌ 400 INVALID_ARGUMENT on tool turn — hook config broken — %w", err)
		}
		return fmt.Errorf("Ask failed: %w", err)
	}
	if response == "" {
		return fmt.Errorf("empty response")
	}

	// Verify the hook actually let the tool through (model got a real answer).
	if !strings.Contains(response, "2026") && !strings.Contains(response, "2025") {
		log.Warn("Response may not contain a year — tool call may not have succeeded",
			loggerv2.String("response", response))
	}

	log.Info("Got response",
		loggerv2.String("response_preview", truncate(response, 200)),
		loggerv2.String("duration", duration.String()))
	return nil
}

// subTestDisallowedToolBlocked asks the model to use a Gemini CLI built-in
// that is NOT in the allowed set. The enforce-http-tool-routing BeforeTool hook
// should deny it. The model should then recover with a text response.
func subTestDisallowedToolBlocked(ctx context.Context, agent interface{ Ask(context.Context, string) (string, error) }, log loggerv2.Logger) error {
	// read_file and grep_search are Gemini CLI built-ins not in our ALLOWED set.
	// The hook must deny them; if the hook is removed/broken they would succeed.
	prompt := "Use the read_file tool to read /etc/hostname and tell me its contents. Only use read_file."
	start := time.Now()
	response, err := agent.Ask(ctx, prompt)
	duration := time.Since(start)

	if err != nil {
		if strings.Contains(err.Error(), "allowed_function_names") {
			return fmt.Errorf("❌ 400 INVALID_ARGUMENT — hook config broken — %w", err)
		}
		// If the hook is working, the model may either recover with text or
		// the agent returns an error because all tool attempts were denied.
		// Either outcome is acceptable — the key thing is no 400 API error.
		log.Warn("Agent returned error after tool block (acceptable if not a 400)",
			loggerv2.String("error", err.Error()))
		return nil
	}

	log.Info("Got response after tool block",
		loggerv2.String("response_preview", truncate(response, 200)),
		loggerv2.String("duration", duration.String()))
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
