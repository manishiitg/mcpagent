package mcpclient

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ProtocolType defines the connection protocol
type ProtocolType string

const (
	ProtocolStdio ProtocolType = "stdio"
	ProtocolSSE   ProtocolType = "sse"
	ProtocolHTTP  ProtocolType = "http"
)

// Special server name constants
const (
	// AllServers indicates that all configured MCP servers should be connected
	// This is the default behavior when no specific server is requested
	AllServers = "all"

	// NoServers indicates that no MCP servers should be connected
	// This is used when an agent should work with pure LLM reasoning without any tools
	NoServers = "NO_SERVERS"
)

// PoolConfig defines connection pooling settings
type PoolConfig struct {
	MaxConnections       int           `json:"max_connections"`
	MinConnections       int           `json:"min_connections"`
	MaxIdleTime          time.Duration `json:"max_idle_time"`
	HealthCheckInterval  time.Duration `json:"health_check_interval"`
	ConnectionTimeout    time.Duration `json:"connection_timeout"`
	ReconnectDelay       time.Duration `json:"reconnect_delay"`
	MaxReconnectAttempts int           `json:"max_reconnect_attempts"`
}

// DefaultPoolConfig returns sensible default pooling configuration
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConnections:       20,
		MinConnections:       2,
		MaxIdleTime:          15 * time.Minute, // Increased from 5 to 15 minutes
		HealthCheckInterval:  2 * time.Minute,  // Increased from 30s to 2 minutes
		ConnectionTimeout:    15 * time.Minute, // Increased from 10 minutes to 15 minutes for very slow npx commands
		ReconnectDelay:       2 * time.Second,
		MaxReconnectAttempts: 3,
	}
}

// ServerConfig represents a single MCP server configuration
type ServerConfig struct {
	Name        string            `json:"name"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Description string            `json:"description,omitempty"`
	Protocol    ProtocolType      `json:"protocol"`
	PoolConfig  PoolConfig        `json:"pool_config"`
	// SSE/HTTP specific fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// NewServerConfig creates a new server configuration with defaults
func NewServerConfig(name string, protocol ProtocolType) ServerConfig {
	return ServerConfig{
		Name:       name,
		Protocol:   protocol,
		PoolConfig: DefaultPoolConfig(),
		Headers:    make(map[string]string),
		Env:        make(map[string]string),
	}
}

type MCPServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	Description string            `json:"description,omitempty"`
	Protocol    ProtocolType      `json:"protocol,omitempty"`
	PoolConfig  *PoolConfig       `json:"pool_config,omitempty"`
	// SSE/HTTP specific fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// GetPoolConfig returns the pool configuration, using defaults if not specified
func (c *MCPServerConfig) GetPoolConfig() PoolConfig {
	if c.PoolConfig != nil {
		return *c.PoolConfig
	}
	return DefaultPoolConfig()
}

// GetProtocol returns the protocol type with smart detection
func (c *MCPServerConfig) GetProtocol() ProtocolType {
	// If protocol is explicitly set, use it
	if c.Protocol != "" {
		return c.Protocol
	}

	// Smart detection based on URL
	if c.URL != "" {
		// If URL contains /sse, assume SSE protocol
		if contains(c.URL, "/sse") {
			return ProtocolSSE
		}
		// If URL starts with http:// or https://, assume HTTP
		if contains(c.URL, "http://") || contains(c.URL, "https://") {
			return ProtocolHTTP
		}
	}

	// Default to stdio
	return ProtocolStdio
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(s, substr)
}

type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// LoadConfig loads MCP server configuration from the specified file
func LoadConfig(configPath string) (*MCPConfig, error) {
	// Validate config path (basic check - path comes from trusted source)
	if configPath == "" {
		return nil, fmt.Errorf("config path cannot be empty")
	}

	// DEBUG: Log before os.ReadFile
	fmt.Printf("[LoadConfig] DEBUG: About to read config file: %s\n", configPath)
	startTime := time.Now()

	//nolint:gosec // G304: configPath comes from command-line/config, not user input
	data, err := os.ReadFile(configPath)

	readDuration := time.Since(startTime)
	if err != nil {
		fmt.Printf("[LoadConfig] DEBUG: os.ReadFile failed after %v: %v\n", readDuration, err)
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}
	fmt.Printf("[LoadConfig] DEBUG: os.ReadFile completed in %v, read %d bytes\n", readDuration, len(data))

	// DEBUG: Log before json.Unmarshal
	fmt.Printf("[LoadConfig] DEBUG: About to unmarshal JSON for: %s\n", configPath)
	unmarshalStartTime := time.Now()

	var config MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		unmarshalDuration := time.Since(unmarshalStartTime)
		fmt.Printf("[LoadConfig] DEBUG: json.Unmarshal failed after %v: %v\n", unmarshalDuration, err)
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	unmarshalDuration := time.Since(unmarshalStartTime)
	fmt.Printf("[LoadConfig] DEBUG: json.Unmarshal completed in %v for: %s\n", unmarshalDuration, configPath)

	return &config, nil
}

// LoadMergedConfig loads the merged configuration (base + user additions)
// This mirrors the logic from mcp_config_routes.go to ensure consistency
func LoadMergedConfig(configPath string, logger interface{}) (*MCPConfig, error) {
	userConfigPath := strings.Replace(configPath, ".json", "_user.json", 1)
	fmt.Printf("[LoadMergedConfig] DEBUG: Starting LoadMergedConfig\n")
	fmt.Printf("[LoadMergedConfig] DEBUG: Base config file: %s\n", configPath)
	fmt.Printf("[LoadMergedConfig] DEBUG: User config file: %s\n", userConfigPath)
	startTime := time.Now()

	// Load base config
	fmt.Printf("[LoadMergedConfig] DEBUG: About to load base config: %s\n", configPath)
	baseConfigStartTime := time.Now()
	baseConfig, err := LoadConfig(configPath)
	baseConfigDuration := time.Since(baseConfigStartTime)
	if err != nil {
		fmt.Printf("[LoadMergedConfig] DEBUG: Failed to load base config after %v: %v\n", baseConfigDuration, err)
		return nil, fmt.Errorf("failed to load base config: %w", err)
	}
	fmt.Printf("[LoadMergedConfig] DEBUG: Base config loaded successfully in %v, found %d servers\n", baseConfigDuration, len(baseConfig.MCPServers))

	// Load user additions (if any)
	fmt.Printf("[LoadMergedConfig] DEBUG: About to load user config: %s\n", userConfigPath)
	userConfigStartTime := time.Now()
	userConfig, err := LoadConfig(userConfigPath)
	userConfigDuration := time.Since(userConfigStartTime)
	if err != nil {
		fmt.Printf("[LoadMergedConfig] DEBUG: User config load failed after %v (this is OK if file doesn't exist): %v\n", userConfigDuration, err)
		// User config doesn't exist yet, use empty config
		userConfig = &MCPConfig{MCPServers: make(map[string]MCPServerConfig)}
		if logger != nil {
			// Try to log if logger supports it
			if logFunc, ok := logger.(interface{ Debugf(string, ...interface{}) }); ok {
				logFunc.Debugf("No user config found at %s, using empty user config", userConfigPath)
			}
		}
	} else {
		fmt.Printf("[LoadMergedConfig] DEBUG: User config loaded successfully in %v, found %d servers\n", userConfigDuration, len(userConfig.MCPServers))
	}

	// Merge base config with user additions
	fmt.Printf("[LoadMergedConfig] DEBUG: Starting merge operation\n")
	mergeStartTime := time.Now()
	mergedConfig := &MCPConfig{
		MCPServers: make(map[string]MCPServerConfig),
	}

	// Add base servers first
	for name, server := range baseConfig.MCPServers {
		mergedConfig.MCPServers[name] = server
	}

	// Add user servers (these will override base servers with same name)
	for name, server := range userConfig.MCPServers {
		mergedConfig.MCPServers[name] = server
	}
	mergeDuration := time.Since(mergeStartTime)
	fmt.Printf("[LoadMergedConfig] DEBUG: Merge operation completed in %v\n", mergeDuration)

	if logger != nil {
		// Try to log if logger supports it
		if logFunc, ok := logger.(interface{ Infof(string, ...interface{}) }); ok {
			logFunc.Infof("✅ Merged config: %d base servers + %d user servers = %d total",
				len(baseConfig.MCPServers), len(userConfig.MCPServers), len(mergedConfig.MCPServers))
		}
	}

	totalDuration := time.Since(startTime)
	fmt.Printf("[LoadMergedConfig] DEBUG: LoadMergedConfig completed successfully in %v\n", totalDuration)
	return mergedConfig, nil
}

// GetServer returns the configuration for a specific server
func (c *MCPConfig) GetServer(name string) (MCPServerConfig, error) {
	server, exists := c.MCPServers[name]
	if !exists {
		return MCPServerConfig{}, fmt.Errorf("server '%s' not found in configuration", name)
	}
	return server, nil
}

// ListServers returns all configured server names
func (c *MCPConfig) ListServers() []string {
	var names []string
	for name := range c.MCPServers {
		names = append(names, name)
	}
	return names
}

// SaveConfig writes the MCPConfig to the specified file atomically
func SaveConfig(configPath string, config *MCPConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil { //nolint:gosec // 0644 permissions are intentional for user-accessible config files
		return err
	}
	return os.Rename(tmpPath, configPath)
}

// AddServer adds a new server to the config and saves it
func (c *MCPConfig) AddServer(name string, server MCPServerConfig, configPath string) error {
	c.MCPServers[name] = server
	return SaveConfig(configPath, c)
}

// EditServer edits an existing server in the config and saves it
func (c *MCPConfig) EditServer(name string, server MCPServerConfig, configPath string) error {
	c.MCPServers[name] = server
	return SaveConfig(configPath, c)
}

// RemoveServer removes a server from the config and saves it
func (c *MCPConfig) RemoveServer(name string, configPath string) error {
	delete(c.MCPServers, name)
	return SaveConfig(configPath, c)
}

// ReloadConfig reloads the config from disk
func (c *MCPConfig) ReloadConfig(configPath string) error {
	newConfig, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	c.MCPServers = newConfig.MCPServers
	return nil
}
