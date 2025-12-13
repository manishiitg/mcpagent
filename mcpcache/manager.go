package mcpcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	loggerv2 "mcpagent/logger/v2"
	"mcpagent/mcpcache/codegen"
	"mcpagent/mcpclient"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/mark3labs/mcp-go/mcp"
)

// CacheEntry represents a cached MCP server connection and its metadata
type CacheEntry struct {
	// Server identification
	ServerName string `json:"server_name"`

	// Connection data
	Tools        []llmtypes.Tool `json:"tools"`
	Prompts      []mcp.Prompt    `json:"prompts"`
	Resources    []mcp.Resource  `json:"resources"`
	SystemPrompt string          `json:"system_prompt"`

	// Metadata
	CreatedAt    time.Time              `json:"created_at"`
	LastAccessed time.Time              `json:"last_accessed"`
	TTLMinutes   int                    `json:"ttl_minutes"`
	Protocol     string                 `json:"protocol"`
	ServerInfo   map[string]interface{} `json:"server_info,omitempty"`

	// Cache management
	IsValid      bool   `json:"is_valid"`
	ErrorMessage string `json:"error_message,omitempty"`

	// Tool ownership tracking (for duplicate detection)
	// Maps tool name -> ownership status ("primary" or "duplicate")
	// When multiple servers provide the same tool, only the "primary" server's entry
	// should expose that tool. This prevents duplicate tool names in the agent's tool list.
	ToolOwnership map[string]string `json:"tool_ownership,omitempty"`
}

// IsExpired checks if the cache entry has expired
func (ce *CacheEntry) IsExpired() bool {
	if !ce.IsValid {
		return true
	}
	expirationTime := ce.CreatedAt.Add(time.Duration(ce.TTLMinutes) * time.Minute)
	return time.Now().After(expirationTime)
}

// UpdateAccessTime updates the last accessed timestamp
// DEPRECATED: This method is no longer called to avoid race conditions.
// LastAccessed field is maintained only for historical compatibility.
func (ce *CacheEntry) UpdateAccessTime() {
	ce.LastAccessed = time.Now()
}

// CacheManager manages MCP server connection caching
type CacheManager struct {
	cacheDir             string
	ttlMinutes           int
	logger               loggerv2.Logger
	mu                   sync.RWMutex
	cache                map[string]*CacheEntry // cache key -> entry
	enableCodeGeneration bool                   // Only generate code when code execution mode is enabled
}

// Singleton instance
var (
	instance *CacheManager
	once     sync.Once
)

// GetCacheManager returns the singleton cache manager instance
func GetCacheManager(logger loggerv2.Logger) *CacheManager {
	once.Do(func() {
		// Use environment variable if set, otherwise default to cache/
		cacheDir := os.Getenv("MCP_CACHE_DIR")
		if cacheDir == "" {
			// Default to cache/ directory (works for both local and Docker)
			cacheDir = "/app/cache" // Docker mount point
			if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
				// For local development, use relative path to cache/
				cacheDir = filepath.Join(".", "cache")
			}
		}
		// Get TTL from environment variable, default to 7 days (10080 minutes)
		ttlMinutes := 10080 // 7 days default
		if ttlEnv := os.Getenv("MCP_CACHE_TTL_MINUTES"); ttlEnv != "" {
			if parsedTTL, err := strconv.Atoi(ttlEnv); err == nil && parsedTTL > 0 {
				ttlMinutes = parsedTTL
			} else if logger != nil {
				logger.Warn("Invalid MCP_CACHE_TTL_MINUTES value, using default",
					loggerv2.String("value", ttlEnv),
					loggerv2.Int("default_minutes", ttlMinutes))
			}
		}

		instance = &CacheManager{
			cacheDir:             cacheDir,
			ttlMinutes:           ttlMinutes, // Configurable TTL via environment variable
			logger:               logger,
			cache:                make(map[string]*CacheEntry),
			enableCodeGeneration: false, // Default to false - only enable when code execution mode is active
		}

		// NOTE: Cache directory is created lazily when actually saving entries
		// This prevents unnecessary directory creation when cache is disabled
		// The directory will be created in saveToFile() when needed

		// Load existing cache entries (this will create directory if cache files exist)
		instance.loadExistingCache()
	})
	return instance
}

// SetCodeGenerationEnabled enables or disables code generation in the cache manager
// Code generation should only be enabled when code execution mode is active
func (cm *CacheManager) SetCodeGenerationEnabled(enabled bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.enableCodeGeneration = enabled
	if cm.logger != nil {
		enabledStr := "false"
		if enabled {
			enabledStr = "true"
		}
		cm.logger.Debug("Code generation setting updated",
			loggerv2.String("enabled", enabledStr))
	}
}

// GenerateServerConfigHash creates a hash of the server configuration
// This includes command, args, env vars, URL, headers, and protocol
func GenerateServerConfigHash(config mcpclient.MCPServerConfig) string {
	// Create a deterministic representation of the config
	configData := struct {
		Command  string            `json:"command"`
		Args     []string          `json:"args"`
		Env      map[string]string `json:"env"`
		URL      string            `json:"url"`
		Headers  map[string]string `json:"headers"`
		Protocol string            `json:"protocol"`
	}{
		Command:  config.Command,
		Args:     config.Args,
		Env:      config.Env,
		URL:      config.URL,
		Headers:  config.Headers,
		Protocol: string(config.Protocol),
	}

	// Sort maps for deterministic output
	if configData.Env != nil {
		sortedEnv := make(map[string]string)
		var envKeys []string
		for k := range configData.Env {
			envKeys = append(envKeys, k)
		}
		sort.Strings(envKeys)
		for _, k := range envKeys {
			sortedEnv[k] = configData.Env[k]
		}
		configData.Env = sortedEnv
	}

	if configData.Headers != nil {
		sortedHeaders := make(map[string]string)
		var headerKeys []string
		for k := range configData.Headers {
			headerKeys = append(headerKeys, k)
		}
		sort.Strings(headerKeys)
		for _, k := range headerKeys {
			sortedHeaders[k] = configData.Headers[k]
		}
		configData.Headers = sortedHeaders
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(configData)
	if err != nil {
		// Fallback to simple string representation
		return fmt.Sprintf("config_%s", config.Command)
	}

	// Generate SHA256 hash
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]) // Use full hash to prevent collisions
}

// GenerateUnifiedCacheKey creates a cache key using server name and configuration hash
// This ensures cache is invalidated when server configuration changes
func GenerateUnifiedCacheKey(serverName string, config mcpclient.MCPServerConfig) string {
	// Clean server name
	cleanServerName := strings.TrimSpace(serverName)

	// Generate configuration hash
	configHash := GenerateServerConfigHash(config)

	// Combine server name and config hash
	return fmt.Sprintf("unified_%s_%s", cleanServerName, configHash)
}

// Get retrieves a cache entry if it exists and is valid
func (cm *CacheManager) Get(cacheKey string) (*CacheEntry, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	entry, exists := cm.cache[cacheKey]
	if !exists {
		return nil, false
	}

	// Check if entry is expired
	if entry.IsExpired() {
		age := time.Since(entry.CreatedAt)
		ttl := time.Duration(entry.TTLMinutes) * time.Minute
		cm.logger.Debug("Cache entry expired", loggerv2.String("key", cacheKey))

		// Note: We don't emit expired events here as we don't have tracers available
		// The expiration event would be emitted when the entry is actually cleaned up
		_ = age // Prevent unused variable warning
		_ = ttl // Prevent unused variable warning

		return nil, false
	}

	// NOTE: LastAccessed is no longer updated to avoid race conditions.
	// The field is kept for historical compatibility but is deprecated.
	// Access time tracking was removed to eliminate data races when reading cache entries.

	cm.logger.Debug("Cache hit", loggerv2.String("key", cacheKey))
	return entry, true
}

// Put stores a cache entry using configuration-aware cache key
func (cm *CacheManager) Put(entry *CacheEntry, config mcpclient.MCPServerConfig) error {
	cm.logger.Debug("Put: Acquiring lock", loggerv2.String("server", entry.ServerName))
	cm.mu.Lock()
	cm.logger.Debug("Put: Lock acquired", loggerv2.String("server", entry.ServerName))

	// Use configuration-aware cache key
	cacheKey := GenerateUnifiedCacheKey(entry.ServerName, config)

	// Set LastAccessed only once when storing (no longer updated on reads)
	entry.LastAccessed = time.Now()

	// Store in memory cache
	cm.cache[cacheKey] = entry
	cm.logger.Debug("Put: Stored in memory cache", loggerv2.String("server", entry.ServerName))

	// Get code generation flag while holding the lock (read it directly, no need for RLock)
	shouldGenerateCode := cm.enableCodeGeneration

	// Release lock before calling saveToFile to avoid deadlock
	// saveToFile may need to acquire RLock for code generation check
	cm.logger.Debug("Put: Releasing lock before saveToFile", loggerv2.String("server", entry.ServerName))
	cm.mu.Unlock()

	// Persist to file using configuration-aware naming (without holding the lock)
	cm.logger.Debug("Put: Calling saveToFile", loggerv2.String("server", entry.ServerName))
	err := cm.saveToFile(entry, config, shouldGenerateCode)
	cm.logger.Debug("Put: saveToFile returned", loggerv2.String("server", entry.ServerName), loggerv2.Error(err))
	return err
}

// Invalidate removes a cache entry
func (cm *CacheManager) Invalidate(cacheKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Get server name from cache entry before deleting
	var serverName string
	if entry, exists := cm.cache[cacheKey]; exists {
		serverName = entry.ServerName
	}

	delete(cm.cache, cacheKey)

	// Remove from filesystem
	cacheFile := cm.getCacheFilePath(cacheKey)
	if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove cache file %s: %w", cacheFile, err)
	}

	// Remove generated Go files for this server
	if serverName != "" {
		generatedDir := cm.getGeneratedDir()
		packageName := codegen.GetPackageName(serverName)
		packageDir := filepath.Join(generatedDir, packageName)
		if err := os.RemoveAll(packageDir); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove generated Go files for server",
				loggerv2.Error(err),
				loggerv2.String("server", serverName))
		} else {
			cm.logger.Debug("Removed generated Go files for server", loggerv2.String("server", serverName))
		}

		// Regenerate index file
		if err := codegen.GenerateIndexFile(generatedDir, cm.logger); err != nil {
			cm.logger.Warn("Failed to regenerate index file", loggerv2.Error(err))
		}
	}

	cm.logger.Debug("Invalidated cache entry", loggerv2.String("key", cacheKey))
	return nil
}

// InvalidateByServer invalidates all cache entries for a specific server
func (cm *CacheManager) InvalidateByServer(configPath, serverName string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var keysToRemove []string

	// Find all keys for this server
	for key, entry := range cm.cache {
		if entry.ServerName == serverName {
			keysToRemove = append(keysToRemove, key)
		}
	}

	// Remove entries
	for _, key := range keysToRemove {
		delete(cm.cache, key)

		// Remove from filesystem
		cacheFile := cm.getCacheFilePath(key)
		if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
	}

	// Remove generated Go files for this server
	if len(keysToRemove) > 0 {
		generatedDir := cm.getGeneratedDir()
		packageName := codegen.GetPackageName(serverName)
		packageDir := filepath.Join(generatedDir, packageName)
		if err := os.RemoveAll(packageDir); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove generated Go files for server",
				loggerv2.Error(err),
				loggerv2.String("server", serverName))
		} else {
			cm.logger.Debug("Removed generated Go files for server", loggerv2.String("server", serverName))
		}

		// Regenerate index file
		if err := codegen.GenerateIndexFile(generatedDir, cm.logger); err != nil {
			cm.logger.Warn("Failed to regenerate index file", loggerv2.Error(err))
		}

		cm.logger.Info("Invalidated cache entries for server",
			loggerv2.Int("count", len(keysToRemove)),
			loggerv2.String("server", serverName))
	}

	return nil
}

// GetAllEntries returns all cached entries (for debugging and registry integration)
// Returns deep copies to prevent race conditions from external mutations
func (cm *CacheManager) GetAllEntries() map[string]*CacheEntry {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return deep copies of cache entries to prevent external mutations
	result := make(map[string]*CacheEntry)
	for key, entry := range cm.cache {
		// Create a deep copy of the entry
		entryCopy := *entry // Copy struct fields

		// Deep copy Tools slice
		if entry.Tools != nil {
			entryCopy.Tools = make([]llmtypes.Tool, len(entry.Tools))
			copy(entryCopy.Tools, entry.Tools)
		}

		// Deep copy Prompts slice
		if entry.Prompts != nil {
			entryCopy.Prompts = make([]mcp.Prompt, len(entry.Prompts))
			copy(entryCopy.Prompts, entry.Prompts)
		}

		// Deep copy Resources slice
		if entry.Resources != nil {
			entryCopy.Resources = make([]mcp.Resource, len(entry.Resources))
			copy(entryCopy.Resources, entry.Resources)
		}

		// Deep copy ServerInfo map if it exists
		if entry.ServerInfo != nil {
			entryCopy.ServerInfo = make(map[string]interface{})
			for k, v := range entry.ServerInfo {
				entryCopy.ServerInfo[k] = v
			}
		}

		// Deep copy ToolOwnership map if it exists
		if entry.ToolOwnership != nil {
			entryCopy.ToolOwnership = make(map[string]string)
			for k, v := range entry.ToolOwnership {
				entryCopy.ToolOwnership[k] = v
			}
		}

		result[key] = &entryCopy
	}
	return result
}

// Clear removes all cache entries
func (cm *CacheManager) Clear() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Clear memory cache
	cm.cache = make(map[string]*CacheEntry)

	// Remove all cache files
	return cm.clearCacheDirectory()
}

// GetStats returns cache statistics
func (cm *CacheManager) GetStats() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	totalEntries := len(cm.cache)
	validEntries := 0
	expiredEntries := 0
	totalSize := int64(0)

	for _, entry := range cm.cache {
		if entry.IsValid && !entry.IsExpired() {
			validEntries++
		} else {
			expiredEntries++
		}

		// Estimate size (rough calculation)
		entrySize := len(entry.ServerName) + len(entry.SystemPrompt)
		for _, tool := range entry.Tools {
			entrySize += len(tool.Function.Name) + len(tool.Function.Description)
		}
		totalSize += int64(entrySize)
	}

	return map[string]interface{}{
		"total_entries":   totalEntries,
		"valid_entries":   validEntries,
		"expired_entries": expiredEntries,
		"estimated_size":  totalSize,
		"cache_directory": cm.cacheDir,
		"ttl_minutes":     cm.ttlMinutes,
	}
}

// Cleanup removes expired entries from both memory and filesystem
func (cm *CacheManager) Cleanup() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var expiredKeys []string

	// Find expired entries
	for key, entry := range cm.cache {
		if entry.IsExpired() {
			expiredKeys = append(expiredKeys, key)
		}
	}

	// Remove expired entries
	for _, key := range expiredKeys {
		delete(cm.cache, key)

		// Remove from filesystem
		cacheFile := cm.getCacheFilePath(key)
		if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove expired cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
	}

	if len(expiredKeys) > 0 {
		cm.logger.Info("Cleaned up expired cache entries", loggerv2.Int("count", len(expiredKeys)))
	}

	return nil
}

// loadExistingCache loads cache entries from the filesystem
func (cm *CacheManager) loadExistingCache() {
	// Try to read cache directory - if it doesn't exist, that's fine (lazy creation)
	// Only create directory if cache files actually exist
	files, err := os.ReadDir(cm.cacheDir)
	if err != nil {
		// Directory doesn't exist yet - that's fine, it will be created when saving entries
		if cm.logger != nil {
			cm.logger.Debug("Cache directory does not exist yet (will be created lazily)", loggerv2.String("cache_dir", cm.cacheDir))
		}
		return
	}

	loadedCount := 0

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			cacheFile := filepath.Join(cm.cacheDir, file.Name())
			if entry := cm.loadFromFile(cacheFile); entry != nil {
				// Use filename as cache key (config-aware format)
				fileName := strings.TrimSuffix(file.Name(), ".json")
				cm.cache[fileName] = entry
				loadedCount++
				if cm.logger != nil {
					cm.logger.Debug("Loaded cache entry", loggerv2.String("file", fileName))
				}

				// Ensure Go code is generated for this cache entry if it's missing
				// This handles cases where cache exists but generated code was deleted
				// Only generate code if code generation is enabled (code execution mode)
				cm.mu.RLock()
				shouldGenerateCode := cm.enableCodeGeneration
				cm.mu.RUnlock()

				if shouldGenerateCode && entry.IsValid && len(entry.Tools) > 0 {
					generatedDir := cm.getGeneratedDir()
					packageName := codegen.GetPackageName(entry.ServerName)
					packageDir := filepath.Join(generatedDir, packageName)

					// Check if code already exists
					if _, err := os.Stat(packageDir); os.IsNotExist(err) {
						// Code doesn't exist - generate it
						if cm.logger != nil {
							cm.logger.Debug("Code missing for cached server, generating", loggerv2.String("server", entry.ServerName))
						}
						entryForCodeGen := &codegen.CacheEntryForCodeGen{
							ServerName: entry.ServerName,
							Tools:      entry.Tools,
						}
						// Use default 5-minute timeout for cache manager (same as agent default)
						defaultTimeout := 5 * time.Minute
						if err := codegen.GenerateServerToolsCode(entryForCodeGen, entry.ServerName, generatedDir, cm.logger, defaultTimeout); err != nil {
							if cm.logger != nil {
								cm.logger.Warn("Failed to generate code for cached server",
									loggerv2.Error(err),
									loggerv2.String("server", entry.ServerName))
							}
							// Don't fail cache load if code generation fails
						} else if cm.logger != nil {
							cm.logger.Debug("Generated code for cached server", loggerv2.String("server", entry.ServerName))
						}
					}
				}
			}
		}
	}

	if loadedCount > 0 && cm.logger != nil {
		cm.logger.Info("Loaded cache entries from filesystem", loggerv2.Int("count", loadedCount))
	}
}

// saveToFile persists a cache entry to the filesystem using configuration-aware naming
// shouldGenerateCode is passed in to avoid needing to acquire RLock (which would deadlock if called from Put with write lock)
func (cm *CacheManager) saveToFile(entry *CacheEntry, config mcpclient.MCPServerConfig, shouldGenerateCode bool) error {
	// Use configuration-aware cache key for file naming
	cacheFile := cm.getCacheFilePath(GenerateUnifiedCacheKey(entry.ServerName, config))

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	// Write to file
	cm.logger.Debug("About to write cache file", loggerv2.String("file", cacheFile), loggerv2.String("server", entry.ServerName), loggerv2.Int("data_size", len(data)))
	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		cm.logger.Error("Failed to write cache file", err, loggerv2.String("file", cacheFile), loggerv2.String("server", entry.ServerName))
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	// Verify file was written
	if stat, err := os.Stat(cacheFile); err != nil {
		cm.logger.Error("Cache file not found after write", err, loggerv2.String("file", cacheFile), loggerv2.String("server", entry.ServerName))
		return fmt.Errorf("cache file not found after write: %w", err)
	} else {
		cm.logger.Info("Saved cache entry to file",
			loggerv2.String("file", cacheFile),
			loggerv2.String("server", entry.ServerName),
			loggerv2.Int("file_size", int(stat.Size())))
	}

	// Generate Go code for tools (only if code generation is enabled)
	// Code generation is only needed in code execution mode
	// Note: shouldGenerateCode is passed as parameter to avoid deadlock (no need to acquire lock here)
	cm.logger.Debug("Checking code generation", loggerv2.String("server", entry.ServerName), loggerv2.String("enabled", fmt.Sprintf("%v", shouldGenerateCode)))

	if shouldGenerateCode {
		cm.logger.Debug("Code generation enabled, generating code", loggerv2.String("server", entry.ServerName))
		generatedDir := cm.getGeneratedDir()
		entryForCodeGen := &codegen.CacheEntryForCodeGen{
			ServerName: entry.ServerName,
			Tools:      entry.Tools,
		}
		// Use default 5-minute timeout for cache manager (same as agent default)
		defaultTimeout := 5 * time.Minute
		if err := codegen.GenerateServerToolsCode(entryForCodeGen, entry.ServerName, generatedDir, cm.logger, defaultTimeout); err != nil {
			cm.logger.Warn("Failed to generate Go code for server",
				loggerv2.Error(err),
				loggerv2.String("server", entry.ServerName))
			// Don't fail cache save if code generation fails
		}
		cm.logger.Debug("Code generation completed", loggerv2.String("server", entry.ServerName))
	} else {
		cm.logger.Debug("Code generation disabled, skipping", loggerv2.String("server", entry.ServerName))
	}

	cm.logger.Debug("saveToFile completed", loggerv2.String("server", entry.ServerName))
	return nil
}

// loadFromFile loads a cache entry from the filesystem
func (cm *CacheManager) loadFromFile(cacheFile string) *CacheEntry {
	//nolint:gosec // G304: cacheFile path is generated internally from validated inputs
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		if cm.logger != nil {
			cm.logger.Debug("Failed to read cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
		return nil
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		if cm.logger != nil {
			cm.logger.Warn("Failed to unmarshal cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
		return nil
	}

	// Check if entry is still valid
	if entry.IsExpired() {
		cm.logger.Debug("Loaded expired cache entry", loggerv2.String("file", cacheFile))
		// Don't return expired entries - attempt to clean up expired file
		if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
			// Log warning but don't fail - expired entry won't be used anyway
			cm.logger.Warn("Failed to remove expired cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
		return nil
	}

	return &entry
}

// ReloadFromDisk reloads a specific cache entry from disk and updates the in-memory cache
func (cm *CacheManager) ReloadFromDisk(cacheKey string) *CacheEntry {
	cacheFile := cm.getCacheFilePath(cacheKey)

	// Check if file exists (outside lock)
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		if cm.logger != nil {
			cm.logger.Debug("Cache file does not exist", loggerv2.String("file", cacheFile))
		}
		return nil
	}

	// Load the entry from disk (outside lock - this is the expensive I/O operation)
	entry := cm.loadFromFile(cacheFile)
	if entry == nil {
		if cm.logger != nil {
			cm.logger.Debug("Failed to load cache entry from disk", loggerv2.String("file", cacheFile))
		}
		return nil
	}

	// Only lock for the memory update (minimal lock time)
	cm.mu.Lock()
	cm.cache[cacheKey] = entry
	cm.mu.Unlock()

	if cm.logger != nil {
		cm.logger.Debug("Reloaded cache entry from disk",
			loggerv2.String("key", cacheKey),
			loggerv2.Int("tools_count", len(entry.Tools)))
	}

	return entry
}

// getCacheFilePath returns the filesystem path for a cache key
func (cm *CacheManager) getCacheFilePath(cacheKey string) string {
	return filepath.Join(cm.cacheDir, fmt.Sprintf("%s.json", cacheKey))
}

// getGeneratedDir returns the path to the generated/ directory
// Only creates the directory if code generation is enabled
func (cm *CacheManager) getGeneratedDir() string {
	// Use shared utility for path calculation (single source of truth)
	path := GetGeneratedDirPath()

	// Only create directory if code generation is enabled
	// This prevents unnecessary directory creation in simple agent mode
	cm.mu.RLock()
	shouldCreate := cm.enableCodeGeneration
	cm.mu.RUnlock()

	if shouldCreate {
		_ = EnsureGeneratedDir(path, cm.logger)
	}

	return path
}

// clearCacheDirectory removes all files from the cache directory
func (cm *CacheManager) clearCacheDirectory() error {
	files, err := os.ReadDir(cm.cacheDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		filePath := filepath.Join(cm.cacheDir, file.Name())
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove cache file",
				loggerv2.Error(err),
				loggerv2.String("file", filePath))
		}
	}

	return nil
}

// GetCacheDirectory returns the cache directory path
func (cm *CacheManager) GetCacheDirectory() string {
	return cm.cacheDir
}

// SetTTL sets the TTL for cache entries (in minutes)
func (cm *CacheManager) SetTTL(minutes int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.ttlMinutes = minutes
}

// GetTTL returns the current TTL setting
func (cm *CacheManager) GetTTL() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.ttlMinutes
}
