package llm

import (
	"context"
	"fmt"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
)

// Re-export Provider type and constants from llm-providers
type Provider = llmproviders.Provider

const (
	ProviderBedrock           = llmproviders.ProviderBedrock
	ProviderOpenAI            = llmproviders.ProviderOpenAI
	ProviderAnthropic         = llmproviders.ProviderAnthropic
	ProviderOpenRouter        = llmproviders.ProviderOpenRouter
	ProviderVertex            = llmproviders.ProviderVertex
	ProviderAzure             = llmproviders.ProviderAzure
	ProviderZAI               = llmproviders.ProviderZAI
	ProviderClaudeCode        = llmproviders.ProviderClaudeCode
	ProviderGeminiCLI         = llmproviders.ProviderGeminiCLI
	ProviderCodexCLI          = llmproviders.ProviderCodexCLI
	ProviderMiniMax           = llmproviders.ProviderMiniMax
	ProviderMiniMaxCodingPlan = llmproviders.ProviderMiniMaxCodingPlan
)

const (
	MetadataKeyMCPConfig                  = "mcp_config"
	MetadataKeyDangerouslySkipPermissions = "dangerously_skip_permissions"
	MetadataKeyTools                      = "claude_code_tools"
)

// Config holds configuration for LLM initialization (agent_go version)
// This is kept for backward compatibility and converted to llm-providers Config internally
type Config struct {
	Provider    Provider
	ModelID     string
	Temperature float64
	Tracers     []observability.Tracer
	TraceID     observability.TraceID
	// Fallback configuration for rate limiting
	FallbackModels []string
	MaxRetries     int
	// Logger for structured logging
	Logger loggerv2.Logger
	// Context for LLM initialization (optional, uses background with timeout if not provided)
	Context context.Context
	// API keys for providers (optional, falls back to environment variables if not provided)
	APIKeys *ProviderAPIKeys
}

// ProviderAPIKeys is the canonical API key holder — aliased from multi-llm-provider-go.
// Add new provider fields to llmproviders.ProviderAPIKeys, not here.
type ProviderAPIKeys = llmproviders.ProviderAPIKeys

// AzureAPIConfig is aliased from multi-llm-provider-go.
type AzureAPIConfig = llmproviders.AzureAPIConfig

// BedrockConfig is aliased from multi-llm-provider-go.
type BedrockConfig = llmproviders.BedrockConfig

// LoggerAdapter adapts v2.Logger to interfaces.Logger
type LoggerAdapter struct {
	logger loggerv2.Logger
}

// NewLoggerAdapter creates a new logger adapter
func NewLoggerAdapter(logger loggerv2.Logger) *LoggerAdapter {
	return &LoggerAdapter{logger: logger}
}

// Infof implements interfaces.Logger
func (l *LoggerAdapter) Infof(format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Info(fmt.Sprintf(format, v...))
}

// Errorf implements interfaces.Logger
func (l *LoggerAdapter) Errorf(format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Error(fmt.Sprintf(format, v...), nil)
}

// Debugf implements interfaces.Logger
func (l *LoggerAdapter) Debugf(format string, args ...interface{}) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Debug(fmt.Sprintf(format, args...))
}

// convertConfig converts agent_go Config to llm-providers Config
func convertConfig(config Config) llmproviders.Config {
	// Create EventEmitterAdapter from tracers
	var eventEmitter interfaces.EventEmitter
	if len(config.Tracers) > 0 {
		eventEmitter = NewEventEmitterAdapter(config.Tracers)
	} else {
		// Create a no-op event emitter if no tracers
		eventEmitter = NewEventEmitterAdapter(nil)
	}

	// Create LoggerAdapter from ExtendedLogger
	var logger interfaces.Logger
	if config.Logger != nil {
		logger = NewLoggerAdapter(config.Logger)
	} else {
		// Create a no-op logger if none provided
		logger = NewLoggerAdapter(nil)
	}

	// API keys — same underlying type (alias), so clone directly.
	providerAPIKeys := config.APIKeys.Clone()

	return llmproviders.Config{
		Provider:       llmproviders.Provider(config.Provider),
		ModelID:        config.ModelID,
		Temperature:    config.Temperature,
		EventEmitter:   eventEmitter,
		TraceID:        interfaces.TraceID(config.TraceID),
		FallbackModels: config.FallbackModels,
		MaxRetries:     config.MaxRetries,
		Logger:         logger,
		Context:        config.Context,
		APIKeys:        providerAPIKeys,
	}
}

// InitializeLLM creates and initializes an LLM based on the provider configuration
// This function maintains backward compatibility by accepting agent_go Config
// and converting it to llm-providers Config internally
func InitializeLLM(config Config) (llmtypes.Model, error) {
	// Convert agent_go Config to llm-providers Config
	externalConfig := convertConfig(config)

	// Call llm-providers InitializeLLM (already returns llmtypes.Model)
	llm, err := llmproviders.InitializeLLM(externalConfig)
	if err != nil {
		return nil, err
	}

	// Wrap the returned LLM to maintain backward compatibility with agent_go-specific fields
	return wrapProviderAwareLLM(llm, config.Provider, config.ModelID, config.Tracers, config.TraceID, config.Logger, config.APIKeys), nil
}

// wrapProviderAwareLLM wraps the llm-providers Model to maintain backward compatibility
// Since both packages now use the same llmtypes, no conversion is needed
func wrapProviderAwareLLM(llm llmtypes.Model, provider Provider, modelID string, tracers []observability.Tracer, traceID observability.TraceID, logger loggerv2.Logger, apiKeys *ProviderAPIKeys) *ProviderAwareLLM {
	return &ProviderAwareLLM{
		Model:    llm,
		provider: provider,
		modelID:  modelID,
		tracers:  tracers,
		traceID:  traceID,
		logger:   logger,
		apiKeys:  apiKeys,
	}
}

// ProviderAwareLLM is a wrapper around LLM that preserves provider information
// This maintains backward compatibility with agent_go code
type ProviderAwareLLM struct {
	llmtypes.Model
	provider Provider
	modelID  string
	tracers  []observability.Tracer
	traceID  observability.TraceID
	logger   loggerv2.Logger
	apiKeys  *ProviderAPIKeys
}

// NewProviderAwareLLM creates a new provider-aware LLM wrapper
// This maintains backward compatibility with existing agent_go code
func NewProviderAwareLLM(llm llmtypes.Model, provider Provider, modelID string, tracers []observability.Tracer, traceID observability.TraceID, logger loggerv2.Logger, apiKeys *ProviderAPIKeys) *ProviderAwareLLM {
	return &ProviderAwareLLM{
		Model:    llm,
		provider: provider,
		modelID:  modelID,
		tracers:  tracers,
		traceID:  traceID,
		logger:   logger,
		apiKeys:  apiKeys,
	}
}

// GetProvider returns the provider of this LLM
func (p *ProviderAwareLLM) GetProvider() Provider {
	return p.provider
}

// GetModelID returns the model ID of this LLM
func (p *ProviderAwareLLM) GetModelID() string {
	return p.modelID
}

// GetAPIKeys returns the API keys used by this LLM
// This allows the agent to automatically extract and reuse keys from the LLM
func (p *ProviderAwareLLM) GetAPIKeys() *ProviderAPIKeys {
	return p.apiKeys
}

// GenerateContent wraps the underlying LLM's GenerateContent method
// This maintains backward compatibility and adds OpenRouter usage parameter logic
func (p *ProviderAwareLLM) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Automatically add usage parameter for OpenRouter requests to get cache token information
	if p.provider == ProviderOpenRouter {
		if p.logger != nil {
			p.logger.Info("Adding OpenRouter usage parameter for cache token information")
		}
		options = append(options, WithOpenRouterUsage())
	}

	// Call the underlying LLM (which is already a ProviderAwareLLM from llm-providers)
	return p.Model.GenerateContent(ctx, messages, options...)
}

// SearchWeb calls a model's native web search capability when available.
func SearchWeb(ctx context.Context, model llmtypes.Model, query string, options ...CallOption) (string, error) {
	if wrapped, ok := model.(*ProviderAwareLLM); ok {
		return llmproviders.SearchWeb(ctx, wrapped.Model, query, options...)
	}
	return llmproviders.SearchWeb(ctx, model, query, options...)
}

// WithOpenRouterUsage enables usage parameter for OpenRouter requests to get cache token information
func WithOpenRouterUsage() CallOption {
	return func(opts *CallOptions) {
		// Set the usage parameter in the request metadata
		if opts.Metadata == nil {
			opts.Metadata = &llmtypes.Metadata{
				Usage: &llmtypes.UsageMetadata{Include: true},
			}
		} else {
			if opts.Metadata.Usage == nil {
				opts.Metadata.Usage = &llmtypes.UsageMetadata{Include: true}
			} else {
				opts.Metadata.Usage.Include = true
			}
		}
	}
}

// WithMCPConfig sets the MCP configuration JSON string for the Claude Code adapter session.
func WithMCPConfig(config string) CallOption {
	return llmproviders.WithMCPConfig(config)
}

// WithDangerouslySkipPermissions enables the --dangerously-skip-permissions flag for the Claude Code CLI.
// CAUTION: This allows the agent to execute any tool without user confirmation.
func WithDangerouslySkipPermissions() CallOption {
	return llmproviders.WithDangerouslySkipPermissions()
}

// WithClaudeCodeSettings sets the --settings flag for the Claude Code CLI.
// It accepts either a JSON string or a file path.
func WithClaudeCodeSettings(settings string) CallOption {
	return llmproviders.WithClaudeCodeSettings(settings)
}

// WithClaudeCodeTools sets the --tools flag for the Claude Code CLI.
// Use "" to disable all built-in tools.
func WithClaudeCodeTools(tools string) CallOption {
	return llmproviders.WithClaudeCodeTools(tools)
}

// WithAllowedTools sets the --allowed-tools flag for the Claude Code CLI.
// Example: "mcp__api-bridge__*" to allow all tools from the bridge.
func WithAllowedTools(tools string) CallOption {
	return llmproviders.WithAllowedTools(tools)
}

// WithMaxTurns sets the --max-turns flag for the Claude Code CLI.
// Limits the number of agentic turns. Claude Code exits with an error when the limit is reached.
func WithMaxTurns(maxTurns int) CallOption {
	return llmproviders.WithMaxTurns(maxTurns)
}

// WithResumeSessionID sets the --resume flag so the Claude Code CLI resumes
// an existing session instead of starting a new one.
func WithResumeSessionID(id string) CallOption {
	return llmproviders.WithResumeSessionID(id)
}

// WithClaudeCodeEffort sets the --effort flag for the Claude Code CLI.
// Values: "low", "medium", "high", "max"
func WithClaudeCodeEffort(level string) CallOption {
	return llmproviders.WithClaudeCodeEffort(level)
}

// --- Gemini CLI Wrapper Functions ---

// WithGeminiResumeSessionID sets the --resume flag so the Gemini CLI resumes
// an existing session instead of starting a new one.
func WithGeminiResumeSessionID(id string) CallOption {
	return llmproviders.WithGeminiResumeSessionID(id)
}

// WithGeminiApprovalMode sets the --approval-mode flag for the Gemini CLI.
func WithGeminiApprovalMode(mode string) CallOption {
	return llmproviders.WithGeminiApprovalMode(mode)
}

// WithGeminiSystemPromptFile sets the GEMINI_SYSTEM_MD environment variable path.
func WithGeminiSystemPromptFile(path string) CallOption {
	return llmproviders.WithGeminiSystemPromptFile(path)
}

// WithGeminiProjectSettings writes a .gemini/settings.json in a temp directory
// and runs the Gemini CLI from there. Controls tool restrictions and MCP bridge config.
func WithGeminiProjectSettings(settingsJSON string) CallOption {
	return llmproviders.WithGeminiProjectSettings(settingsJSON)
}

// WithGeminiAllowedTools sets the deprecated --allowed-tools flag for the Gemini CLI.
// Prefer WithGeminiProjectSettings plus Policy Engine rules instead.
func WithGeminiAllowedTools(tools string) CallOption {
	return llmproviders.WithGeminiAllowedTools(tools)
}

// WithGeminiProjectDirID sets an explicit project directory ID for the Gemini CLI.
// This ensures resume calls use the same isolated project directory as the original invocation.
func WithGeminiProjectDirID(id string) CallOption {
	return llmproviders.WithGeminiProjectDirID(id)
}

// --- Codex CLI Wrapper Functions ---

// WithCodexResumeSessionID resumes a Codex CLI session by thread ID.
func WithCodexResumeSessionID(id string) CallOption {
	return llmproviders.WithCodexResumeSessionID(id)
}

// WithCodexApprovalPolicy sets the approval_policy for the Codex CLI.
func WithCodexApprovalPolicy(policy string) CallOption {
	return llmproviders.WithCodexApprovalPolicy(policy)
}

// WithCodexReasoningEffort sets the model_reasoning_effort for the Codex CLI.
func WithCodexReasoningEffort(effort string) CallOption {
	return llmproviders.WithCodexReasoningEffort(effort)
}

// WithCodexDisableShellTool disables the built-in shell tool in Codex CLI.
func WithCodexDisableShellTool() CallOption {
	return llmproviders.WithCodexDisableShellTool()
}

// WithCodexFullAuto enables --full-auto mode for the Codex CLI.
func WithCodexFullAuto() CallOption {
	return llmproviders.WithCodexFullAuto()
}

// WithCodexSandbox sets the --sandbox flag for the Codex CLI.
func WithCodexSandbox(sandbox string) CallOption {
	return llmproviders.WithCodexSandbox(sandbox)
}

// WithCodexProjectDirID sets the working directory for the Codex CLI.
func WithCodexProjectDirID(dir string) CallOption {
	return llmproviders.WithCodexProjectDirID(dir)
}

// WithCodexConfigOverrides passes arbitrary -c key=value overrides to the Codex CLI.
func WithCodexConfigOverrides(overrides []string) CallOption {
	return llmproviders.WithCodexConfigOverrides(overrides)
}

// WithCodexEnableFeatures enables one or more Codex CLI features (comma-separated).
func WithCodexEnableFeatures(features string) CallOption {
	return llmproviders.WithCodexEnableFeatures(features)
}

// Re-export helper functions from llm-providers

// GetDefaultModel returns the default model for each provider from environment variables
func GetDefaultModel(provider Provider) string {
	return llmproviders.GetDefaultModel(llmproviders.Provider(provider))
}

// GetDefaultFallbackModels returns fallback models for each provider from environment variables
func GetDefaultFallbackModels(provider Provider) []string {
	return llmproviders.GetDefaultFallbackModels(llmproviders.Provider(provider))
}

// GetCrossProviderFallbackModels returns cross-provider fallback models (e.g., OpenAI for Bedrock)
func GetCrossProviderFallbackModels(provider Provider) []string {
	return llmproviders.GetCrossProviderFallbackModels(llmproviders.Provider(provider))
}

// ValidateProvider checks if the provider is supported
func ValidateProvider(provider string) (Provider, error) {
	p, err := llmproviders.ValidateProvider(provider)
	return Provider(p), err
}

// Re-export response types from llm-providers
type LLMDefaultsResponse = llmproviders.LLMDefaultsResponse
type APIKeyValidationRequest = llmproviders.APIKeyValidationRequest
type APIKeyValidationResponse = llmproviders.APIKeyValidationResponse

// GetLLMDefaults returns default LLM configurations from environment variables
func GetLLMDefaults() LLMDefaultsResponse {
	return llmproviders.GetLLMDefaults()
}

// ValidateAPIKey validates API keys for OpenRouter, OpenAI, Bedrock, and Vertex
func ValidateAPIKey(req APIKeyValidationRequest) APIKeyValidationResponse {
	return llmproviders.ValidateAPIKey(req)
}

// IsO3O4Model detects o3/o4 models (OpenAI) for conditional logic in agent
func IsO3O4Model(modelID string) bool {
	return llmproviders.IsO3O4Model(modelID)
}
