package grpcserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	mcpagent "mcpagent/agent"
	"mcpagent/llm"
	loggerv2 "mcpagent/logger/v2"
)

// ManagedAgent wraps an agent with metadata for lifecycle management
type ManagedAgent struct {
	ID           string
	SessionID    string
	Agent        *mcpagent.Agent
	Config       AgentConfig
	CreatedAt    time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	capabilities Capabilities
	// CustomTools stores definitions for tools that execute via gRPC stream
	CustomTools []CustomToolDefinition
}

// AgentManager manages the lifecycle of agent instances
type AgentManager struct {
	agents        map[string]*ManagedAgent
	mu            sync.RWMutex
	logger        loggerv2.Logger
	defaultConfig string // Default MCP config path
}

// NewAgentManager creates a new agent manager
func NewAgentManager(logger loggerv2.Logger, defaultConfigPath string) *AgentManager {
	return &AgentManager{
		agents:        make(map[string]*ManagedAgent),
		logger:        logger,
		defaultConfig: defaultConfigPath,
	}
}

// CreateAgent creates a new agent instance with the given configuration
func (m *AgentManager) CreateAgent(parentCtx context.Context, req CreateAgentRequest) (*ManagedAgent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Generate IDs
	agentID := "agent_" + uuid.New().String()[:8]
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "session_" + uuid.New().String()[:8]
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(parentCtx)

	// Determine config path
	configPath := req.Config.MCPConfigPath
	if configPath == "" {
		configPath = m.defaultConfig
	}

	// Initialize LLM
	llmModel, err := m.initializeLLM(ctx, req.Config)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Build agent options
	options := m.buildAgentOptions(req.Config, sessionID)

	// Create the agent
	agent, err := mcpagent.NewAgent(ctx, llmModel, configPath, options...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// Get capabilities
	toolToServer := agent.GetToolToServer()
	tools := make([]string, 0, len(toolToServer))
	serverSet := make(map[string]bool)
	for tool, server := range toolToServer {
		tools = append(tools, fmt.Sprintf("%s:%s", server, tool))
		serverSet[server] = true
	}
	servers := make([]string, 0, len(serverSet))
	for server := range serverSet {
		servers = append(servers, server)
	}

	managed := &ManagedAgent{
		ID:          agentID,
		SessionID:   sessionID,
		Agent:       agent,
		Config:      req.Config,
		CreatedAt:   time.Now(),
		ctx:         ctx,
		cancel:      cancel,
		CustomTools: req.Config.CustomTools,
		capabilities: Capabilities{
			Tools:   tools,
			Servers: servers,
		},
	}

	m.agents[agentID] = managed
	m.logger.Info("Agent created", loggerv2.String("agent_id", agentID), loggerv2.String("session_id", sessionID))

	return managed, nil
}

// GetAgent retrieves an agent by ID
func (m *AgentManager) GetAgent(agentID string) (*ManagedAgent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agent, ok := m.agents[agentID]
	return agent, ok
}

// DestroyAgent destroys an agent and cleans up its resources
func (m *AgentManager) DestroyAgent(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	// Cancel context and close agent
	agent.cancel()
	agent.Agent.Close()
	delete(m.agents, agentID)

	m.logger.Info("Agent destroyed", loggerv2.String("agent_id", agentID))
	return nil
}

// ListAgents returns a list of all active agents
func (m *AgentManager) ListAgents() []AgentSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agents := make([]AgentSummary, 0, len(m.agents))
	for _, agent := range m.agents {
		agents = append(agents, AgentSummary{
			AgentID:   agent.ID,
			SessionID: agent.SessionID,
			Status:    "ready",
			CreatedAt: agent.CreatedAt,
		})
	}
	return agents
}

// DestroyAll destroys all agents
func (m *AgentManager) DestroyAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, agent := range m.agents {
		agent.cancel()
		agent.Agent.Close()
		delete(m.agents, id)
	}
	m.logger.Info("All agents destroyed")
}

// GetCapabilities returns the capabilities of an agent
func (m *AgentManager) GetCapabilities(agentID string) (*Capabilities, error) {
	agent, ok := m.GetAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}
	return &agent.capabilities, nil
}

// initializeLLM creates an LLM instance based on config
func (m *AgentManager) initializeLLM(ctx context.Context, config AgentConfig) (llmtypes.Model, error) {
	// Determine provider
	provider := llm.ProviderOpenAI // default
	switch config.Provider {
	case "bedrock":
		provider = llm.ProviderBedrock
	case "openai":
		provider = llm.ProviderOpenAI
	case "anthropic":
		provider = llm.ProviderAnthropic
	case "openrouter":
		provider = llm.ProviderOpenRouter
	case "vertex":
		provider = llm.ProviderVertex
	}

	// Default model IDs per provider
	modelID := config.ModelID
	if modelID == "" {
		switch provider {
		case llm.ProviderOpenAI:
			modelID = "gpt-4o"
		case llm.ProviderBedrock:
			modelID = "anthropic.claude-sonnet-4-20250514-v1:0"
		case llm.ProviderAnthropic:
			modelID = "claude-sonnet-4-20250514"
		default:
			modelID = "gpt-4o"
		}
	}

	temperature := 0.0
	if config.Temperature != nil {
		temperature = *config.Temperature
	}

	llmConfig := llm.Config{
		Provider:    provider,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      m.logger,
		Context:     ctx,
	}

	return llm.InitializeLLM(llmConfig)
}

// buildAgentOptions converts config to agent options
func (m *AgentManager) buildAgentOptions(config AgentConfig, sessionID string) []mcpagent.AgentOption {
	// Determine provider
	provider := llm.ProviderOpenAI // default
	switch config.Provider {
	case "bedrock":
		provider = llm.ProviderBedrock
	case "openai":
		provider = llm.ProviderOpenAI
	case "anthropic":
		provider = llm.ProviderAnthropic
	case "openrouter":
		provider = llm.ProviderOpenRouter
	case "vertex":
		provider = llm.ProviderVertex
	}

	options := []mcpagent.AgentOption{
		mcpagent.WithLogger(m.logger),
		mcpagent.WithSessionID(sessionID),
		mcpagent.WithProvider(provider),
	}

	if config.MaxTurns > 0 {
		options = append(options, mcpagent.WithMaxTurns(config.MaxTurns))
	}

	if config.Temperature != nil {
		options = append(options, mcpagent.WithTemperature(*config.Temperature))
	}

	if config.SystemPrompt != "" {
		options = append(options, mcpagent.WithSystemPrompt(config.SystemPrompt))
	}

	if len(config.SelectedServers) > 0 {
		options = append(options, mcpagent.WithSelectedServers(config.SelectedServers))
	}

	if len(config.SelectedTools) > 0 {
		options = append(options, mcpagent.WithSelectedTools(config.SelectedTools))
	}

	if config.EnableContextSummarization {
		options = append(options, mcpagent.WithContextSummarization(true))
	}

	if config.EnableContextOffloading {
		options = append(options, mcpagent.WithContextOffloading(true))
	}

	if config.EnableStreaming {
		options = append(options, mcpagent.WithStreaming(true))
	}

	return options
}
