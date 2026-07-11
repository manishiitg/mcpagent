package main

import (
	"sync"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var (
	testLogger   loggerv2.Logger
	testLoggerMu sync.RWMutex
)

// InitTestLogger initializes the shared test logger with specified configuration
// This creates a single logger instance that all tests can use
// Uses v2.Logger for thread-safe, structured logging
func InitTestLogger(logFile string, level string) {
	testLoggerMu.Lock()
	defer testLoggerMu.Unlock()

	cfg := loggerv2.Config{
		Level:      level,
		Format:     "text",
		Output:     "stdout",
		EnableFile: logFile != "",
		FilePath:   logFile,
	}

	l, err := loggerv2.New(cfg)
	if err != nil {
		// Fallback to default logger if there's an error
		fallbackCfg := loggerv2.Config{
			Level:      "info",
			Format:     "text",
			Output:     "stdout",
			EnableFile: true,
			FilePath:   "cmd/testing/logs/test-fallback.log",
		}
		l, _ = loggerv2.New(fallbackCfg)
	}
	testLogger = l
}

// GetTestLogger returns the shared test logger instance
// If no logger has been initialized, creates a default one
// Returns v2.Logger for thread-safe access
func GetTestLogger() loggerv2.Logger {
	testLoggerMu.RLock()
	if testLogger != nil {
		testLoggerMu.RUnlock()
		return testLogger
	}
	testLoggerMu.RUnlock()

	// Double-check locking pattern
	testLoggerMu.Lock()
	defer testLoggerMu.Unlock()

	if testLogger == nil {
		// Create default test logger if none exists
		cfg := loggerv2.Config{
			Level:      "info",
			Format:     "text",
			Output:     "stdout",
			EnableFile: true,
			FilePath:   "cmd/testing/logs/test-default.log",
		}
		l, _ := loggerv2.New(cfg)
		testLogger = l
	}
	return testLogger
}

// SetTestLogger allows tests to override the shared logger
// Useful for testing different logger configurations
// Thread-safe setter
func SetTestLogger(l loggerv2.Logger) {
	testLoggerMu.Lock()
	defer testLoggerMu.Unlock()
	testLogger = l
}
