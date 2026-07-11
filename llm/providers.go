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
	ProviderKimi              = llmproviders.ProviderKimi
	ProviderClaudeCode        = llmproviders.ProviderClaudeCode
	ProviderCodexCLI          = llmproviders.ProviderCodexCLI
	ProviderCursorCLI         = llmproviders.ProviderCursorCLI
	ProviderAgyCLI            = llmproviders.ProviderAgyCLI
	ProviderPiCLI             = llmproviders.ProviderPiCLI
	ProviderMiniMax           = llmproviders.ProviderMiniMax
	ProviderMiniMaxCodingPlan = llmproviders.ProviderMiniMaxCodingPlan
	ProviderElevenLabs        = llmproviders.ProviderElevenLabs
	ProviderDeepgram          = llmproviders.ProviderDeepgram
)

const (
	ClaudeCodeTransportTmux         = llmproviders.ClaudeCodeTransportTmux
	ClaudeCodeTransportExperimental = llmproviders.ClaudeCodeTransportExperimental
	ClaudeCodeTransportPrint        = llmproviders.ClaudeCodeTransportPrint
)

type CodingAgentTransport = llmproviders.CodingAgentTransport
type CodingAgentProviderContract = llmproviders.CodingAgentProviderContract

const (
	CodingAgentTransportTmux       = llmproviders.CodingAgentTransportTmux
	CodingAgentTransportStructured = llmproviders.CodingAgentTransportStructured
)

func GetCodingAgentProviderContract(provider Provider, modelID string) (CodingAgentProviderContract, bool) {
	return llmproviders.GetCodingAgentProviderContract(provider, modelID)
}

func IsCodingAgentProvider(provider Provider, modelID string) bool {
	return llmproviders.IsCodingAgentProvider(provider, modelID)
}

func IsTmuxCodingAgentProvider(provider Provider, modelID string) bool {
	return llmproviders.IsTmuxCodingAgentProvider(provider, modelID)
}

func CodingAgentProviderContracts() []CodingAgentProviderContract {
	return llmproviders.CodingAgentProviderContracts()
}

func CodingAgentInteractiveSessionOption(provider Provider, ownerSessionID string) llmtypes.CallOption {
	return llmproviders.CodingAgentInteractiveSessionOption(provider, ownerSessionID)
}

func CodingAgentPersistentInteractiveOption(provider Provider, enabled bool) llmtypes.CallOption {
	return llmproviders.CodingAgentPersistentInteractiveOption(provider, enabled)
}

func CodingAgentWorkingDirOption(provider Provider, workingDir string) llmtypes.CallOption {
	return llmproviders.CodingAgentWorkingDirOption(provider, workingDir)
}

func CodingAgentProjectDirIDOption(provider Provider, projectDirID string) llmtypes.CallOption {
	return llmproviders.CodingAgentProjectDirIDOption(provider, projectDirID)
}

func CodingAgentProjectInstructionOnlyOption(provider Provider, enabled bool) llmtypes.CallOption {
	return llmproviders.CodingAgentProjectInstructionOnlyOption(provider, enabled)
}

func NativeResumeOption(provider Provider, sessionID string) llmtypes.CallOption {
	return llmproviders.NativeResumeOption(provider, sessionID)
}

type CodingAgentContinuationError = llmproviders.CodingAgentContinuationError
type CodingAgentContinuationErrorKind = llmproviders.CodingAgentContinuationErrorKind

const (
	CodingAgentContinuationErrorNonApplicable  = llmproviders.CodingAgentContinuationErrorNonApplicable
	CodingAgentContinuationErrorNonContinuable = llmproviders.CodingAgentContinuationErrorNonContinuable
	CodingAgentContinuationErrorStaleHandle    = llmproviders.CodingAgentContinuationErrorStaleHandle
)

func ContinueCodingAgentSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, message string, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return llmproviders.ContinueCodingAgentSession(ctx, model, handle, message, options...)
}

func StartCodingAgentTransportSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return llmproviders.StartCodingAgentTransportSession(ctx, model, handle, options...)
}

func StartCodingAgentTmuxSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return llmproviders.StartCodingAgentTransportSession(ctx, model, handle, options...)
}

func SendCodingAgentLiveInput(ctx context.Context, provider Provider, modelID, ownerSessionID, message string) error {
	return llmproviders.SendCodingAgentLiveInput(ctx, provider, modelID, ownerSessionID, message)
}

// SendCodingAgentControlKey injects a tmux control key (e.g. "Escape", "C-c")
// into a currently running tmux-based coding-agent session. Mirrors
// SendCodingAgentLiveInput but sends a raw key instead of text.
func SendCodingAgentControlKey(ctx context.Context, provider Provider, modelID, ownerSessionID, key string) error {
	return llmproviders.SendCodingAgentControlKey(ctx, provider, modelID, ownerSessionID, key)
}

// IsAllowedCodingAgentControlKey reports whether the named tmux key is on the
// whitelist accepted by SendCodingAgentControlKey.
func IsAllowedCodingAgentControlKey(key string) bool {
	return llmproviders.IsAllowedCodingAgentControlKey(key)
}

func IsCodingAgentContinuationError(err error, kind CodingAgentContinuationErrorKind) bool {
	return llmproviders.IsCodingAgentContinuationError(err, kind)
}

const (
	MetadataKeyMCPConfig                  = "mcp_config"
	MetadataKeyDangerouslySkipPermissions = "dangerously_skip_permissions"
	MetadataKeyTools                      = "claude_code_tools"
)

// SendClaudeCodeInput sends user input to a live Claude Code interactive
// session registered for the owning application session. (Renamed from
// SendClaudeCodeExperimentalInput to match the upstream Experimental→Interactive
// rename; no callers used the old name.)
func SendClaudeCodeInput(ctx context.Context, sessionID, message string) error {
	return llmproviders.SendClaudeCodeInput(ctx, sessionID, message)
}

// SendCodexCLIInteractiveInput sends user input to a live Codex CLI interactive
// session registered for the owning application session.
func SendCodexCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return llmproviders.SendCodexCLIInteractiveInput(ctx, sessionID, message)
}

// SendCursorCLIInteractiveInput sends user input to a live Cursor CLI
// interactive session registered for the owning application session.
func SendCursorCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return llmproviders.SendCursorCLIInteractiveInput(ctx, sessionID, message)
}

// SendAgyCLIInteractiveInput sends user input to a live Antigravity CLI
// interactive session registered for the owning application session.
func SendAgyCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return llmproviders.SendAgyCLIInteractiveInput(ctx, sessionID, message)
}

// SendPiCLIInteractiveInput sends user input to a live Pi CLI interactive
// session registered for the owning application session.
func SendPiCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return llmproviders.SendPiCLIInteractiveInput(ctx, sessionID, message)
}

// CleanupPiCLIInteractiveSessions closes all tracked Pi CLI interactive
// sessions.
func CleanupPiCLIInteractiveSessions(ctx context.Context) error {
	return llmproviders.CleanupPiCLIInteractiveSessions(ctx)
}

// ClosePiCLIInteractiveSessionForOwner closes a tracked Pi CLI session by
// owning application session id.
func ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	llmproviders.ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

// ClosePiCLIInteractiveSessionByTmux closes a tracked Pi CLI session by tmux
// session name.
func ClosePiCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	llmproviders.ClosePiCLIInteractiveSessionByTmux(tmuxSessionName, reason)
}

// WithClaudeCodeInteractiveSessionID links a Claude Code experimental run to
// the owning application session so live follow-up input can be sent to it.
func WithClaudeCodeInteractiveSessionID(id string) llmtypes.CallOption {
	return llmproviders.WithClaudeCodeInteractiveSessionID(id)
}

// WithClaudeCodePersistentInteractiveSession keeps the Claude Code experimental
// tmux session alive across completed interactive chat turns.
func WithClaudeCodePersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return llmproviders.WithClaudeCodePersistentInteractiveSession(enabled)
}

// WithClaudeCodeWorkingDir sets the process working directory for Claude Code.
func WithClaudeCodeWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithClaudeCodeWorkingDir(dir)
}

// WithClaudeCodeProjectInstructionOnly makes the Claude Code adapter carry the
// per-session system prompt solely via <workingDir>/CLAUDE.md (auto-loaded as
// project instructions), skipping --system-prompt-file / --append-system-prompt
// so the prompt is applied once instead of doubled. Falls back to the flag if
// the CLAUDE.md projection is disabled or its write fails.
func WithClaudeCodeProjectInstructionOnly(enabled bool) llmtypes.CallOption {
	return llmproviders.WithClaudeCodeProjectInstructionOnly(enabled)
}

// WithCodexProjectInstructionOnly carries the codex system prompt solely via
// the projected AGENTS.md, skipping the developer_instructions /
// model_instructions_file CLI override, so the prompt is applied once instead
// of doubled. Falls back to the override if the projection is disabled/fails.
func WithCodexProjectInstructionOnly(enabled bool) llmtypes.CallOption {
	return llmproviders.WithCodexProjectInstructionOnly(enabled)
}

// WithCodexInteractiveSessionID links a Codex CLI interactive run to the owning
// application session so live follow-up input can be sent to it.
func WithCodexInteractiveSessionID(id string) llmtypes.CallOption {
	return llmproviders.WithCodexInteractiveSessionID(id)
}

// WithCodexPersistentInteractiveSession keeps Codex CLI interactive tmux
// sessions alive across completed chat turns.
func WithCodexPersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return llmproviders.WithCodexPersistentInteractiveSession(enabled)
}

// WithCursorInteractiveSessionID links a Cursor CLI interactive run to the
// owning application session so live follow-up input can be sent to it.
func WithCursorInteractiveSessionID(id string) llmtypes.CallOption {
	return llmproviders.WithCursorInteractiveSessionID(id)
}

// WithCursorPersistentInteractiveSession keeps Cursor CLI interactive tmux
// sessions alive across completed chat turns.
func WithCursorPersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return llmproviders.WithCursorPersistentInteractiveSession(enabled)
}

// WithCursorWorkingDir sets the Cursor Agent CLI workspace/cwd.
func WithCursorWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithCursorWorkingDir(dir)
}

// WithCursorMCPConfig writes a temporary/restored .cursor/mcp.json for Cursor.
func WithCursorMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithCursorMCPConfig(config)
}

// WithCursorProjectConfig writes a temporary/restored .cursor/cli.json for Cursor.
func WithCursorProjectConfig(config string) llmtypes.CallOption {
	return llmproviders.WithCursorProjectConfig(config)
}

// WithCursorForce enables Cursor Agent CLI's --force flag.
func WithCursorForce() llmtypes.CallOption {
	return llmproviders.WithCursorForce()
}

// WithCursorApproveMCPs enables Cursor Agent CLI's --approve-mcps flag, which
// auto-accepts the "approve this MCP server?" TUI consent dialog so bridge
// tool calls do not stall waiting for human approval. Only useful alongside
// WithCursorMCPConfig — without an MCP config there is nothing to approve.
func WithCursorApproveMCPs() llmtypes.CallOption {
	return llmproviders.WithCursorApproveMCPs()
}

// WithCursorAutoApproveWebSearch auto-accepts Cursor Agent CLI's TUI prompts
// for web search and opening URLs during an already-user-initiated agent turn.
// It does not enable --force.
func WithCursorAutoApproveWebSearch() llmtypes.CallOption {
	return llmproviders.WithCursorAutoApproveWebSearch()
}

// WithCursorMode sets Cursor Agent CLI's --mode flag. Leave empty for normal
// agent mode (the default chat path).
//
// CAUTION: "ask" and "plan" are conversational stances, not just "block
// built-in Write/Shell". Cursor hard-refuses natural-language write requests
// with "Switch to Agent mode and ask…", making chat unusable for any turn
// that requires writes. Only safe when prompts explicitly name an MCP tool
// to call (e.g. structured-mode E2E tests). For general chat, leave mode empty.
func WithCursorMode(mode string) llmtypes.CallOption {
	return llmproviders.WithCursorMode(mode)
}

// WithCursorDenyBuiltinTools installs a per-session .cursor/hooks.json that
// denies Cursor's built-in Shell/Read/Edit/Write/etc. tools at the hook
// layer, forcing the agent to route every tool call through the MCP bridge.
// This is the modern equivalent of --mode ask but without the conversational
// hard-refuse behavior — natural-language requests still work; only built-in
// tool calls get vetoed. Pairs cleanly with WithCursorMCPConfig +
// WithCursorApproveMCPs.
func WithCursorDenyBuiltinTools(enabled bool) llmtypes.CallOption {
	return llmproviders.WithCursorDenyBuiltinTools(enabled)
}

// WithCursorSandbox sets Cursor Agent CLI's --sandbox flag ("enabled"/"disabled").
func WithCursorSandbox(mode string) llmtypes.CallOption {
	return llmproviders.WithCursorSandbox(mode)
}

// WithAgyInteractiveSessionID links an Antigravity CLI interactive run to the
// owning application session so live follow-up input can be sent to it.
func WithAgyInteractiveSessionID(id string) llmtypes.CallOption {
	return llmproviders.WithAgyInteractiveSessionID(id)
}

// WithAgyPersistentInteractiveSession keeps Antigravity CLI interactive tmux
// sessions alive across completed chat turns.
func WithAgyPersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return llmproviders.WithAgyPersistentInteractiveSession(enabled)
}

// WithAgyWorkingDir sets the Antigravity CLI workspace/cwd.
func WithAgyWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithAgyWorkingDir(dir)
}

// WithAgyMCPConfig records an Antigravity MCP config candidate.
func WithAgyMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithAgyMCPConfig(config)
}

// WithAgyBridgeOnlyTools writes an Antigravity workspace hook that denies
// built-in tools while leaving configured MCP bridge tools available.
func WithAgyBridgeOnlyTools(enabled bool) llmtypes.CallOption {
	return llmproviders.WithAgyBridgeOnlyTools(enabled)
}

// WithAgyDangerouslySkipPermissions controls agy's skip-permissions flag.
func WithAgyDangerouslySkipPermissions(enabled bool) llmtypes.CallOption {
	return llmproviders.WithAgyDangerouslySkipPermissions(enabled)
}

// WithPiInteractiveSessionID links a Pi CLI interactive run to the owning
// application session so live follow-up input can be sent to it.
func WithPiInteractiveSessionID(id string) llmtypes.CallOption {
	return llmproviders.WithPiInteractiveSessionID(id)
}

// WithPiPersistentInteractiveSession keeps Pi CLI tmux sessions alive across
// completed chat turns.
func WithPiPersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return llmproviders.WithPiPersistentInteractiveSession(enabled)
}

// WithPiResumeSessionID resumes a Pi native session created with --session-id.
func WithPiResumeSessionID(sessionID string) llmtypes.CallOption {
	return llmproviders.WithPiResumeSessionID(sessionID)
}

// WithPiWorkingDir sets the Pi CLI workspace/cwd.
func WithPiWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithPiWorkingDir(dir)
}

// WithPiProvider overrides the Pi upstream provider inferred from the model id.
func WithPiProvider(provider string) llmtypes.CallOption {
	return llmproviders.WithPiProvider(provider)
}

// WithPiMCPConfig records a Pi MCP config candidate.
func WithPiMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithPiMCPConfig(config)
}

// WithPiBridgeOnlyTools disables Pi built-in tools while leaving extension and
// custom tools, including the MCP adapter, enabled.
func WithPiBridgeOnlyTools(enabled bool) llmtypes.CallOption {
	return llmproviders.WithPiBridgeOnlyTools(enabled)
}

// WithPiMCPExtension overrides the Pi MCP adapter extension source.
func WithPiMCPExtension(source string) llmtypes.CallOption {
	return llmproviders.WithPiMCPExtension(source)
}

// WithPiStatuslineExtension overrides the Pi statusline extension source.
func WithPiStatuslineExtension(source string) llmtypes.CallOption {
	return llmproviders.WithPiStatuslineExtension(source)
}

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
	// ClaudeCodeTransport optionally overrides CLAUDE_CODE_TRANSPORT for this
	// initialized Claude Code model.
	ClaudeCodeTransport string
}

// ProviderAPIKeys is the canonical API key holder — aliased from multi-llm-provider-go.
// Add new provider fields to llmproviders.ProviderAPIKeys, not here.
type ProviderAPIKeys = llmproviders.ProviderAPIKeys

// Music generation types are re-exported for agent packages that depend on mcpagent/llm.
type MusicGenerationModel = llmtypes.MusicGenerationModel
type MusicGenerationResponse = llmtypes.MusicGenerationResponse
type GeneratedMusic = llmtypes.GeneratedMusic
type MusicGenerationOptions = llmtypes.MusicGenerationOptions
type MusicGenerationOption = llmtypes.MusicGenerationOption

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
		Provider:            llmproviders.Provider(config.Provider),
		ModelID:             config.ModelID,
		Temperature:         config.Temperature,
		EventEmitter:        eventEmitter,
		TraceID:             interfaces.TraceID(config.TraceID),
		FallbackModels:      config.FallbackModels,
		MaxRetries:          config.MaxRetries,
		Logger:              logger,
		Context:             config.Context,
		APIKeys:             providerAPIKeys,
		ClaudeCodeTransport: config.ClaudeCodeTransport,
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

// --- Codex CLI Wrapper Functions ---

// WithCodexResumeSessionID resumes a Codex CLI session by thread ID.
func WithCodexResumeSessionID(id string) CallOption {
	return llmproviders.WithCodexResumeSessionID(id)
}

// WithCursorResumeSessionID resumes a Cursor CLI chat by session ID — the
// id cursor-agent emits in its stream-json init event (and that is also the
// directory name under ~/.cursor/chats/<md5(cwd)>/<id>). Mirrors the
// claude-code / codex equivalents so a restored chat can pick up
// cursor's native chat memory instead of starting fresh.
func WithCursorResumeSessionID(id string) CallOption {
	return llmproviders.WithCursorResumeSessionID(id)
}

// WithAgyResumeSessionID resumes an Antigravity CLI conversation by id.
func WithAgyResumeSessionID(id string) CallOption {
	return llmproviders.WithAgyResumeSessionID(id)
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

// GetDefaultFallbackModelsForModel returns fallback models for a provider while
// considering the current primary model when provider-specific defaults need it.
func GetDefaultFallbackModelsForModel(provider Provider, modelID string) []string {
	return llmproviders.GetDefaultFallbackModelsForModel(llmproviders.Provider(provider), modelID)
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
