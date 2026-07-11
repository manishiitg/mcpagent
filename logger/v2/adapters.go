package v2

import (
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/mark3labs/mcp-go/util"
)

// ToUtilLogger adapts v2.Logger to util.Logger (MCP-Go interface)
func ToUtilLogger(l Logger) util.Logger {
	return &utilLoggerAdapter{
		logger: l,
	}
}

// utilLoggerAdapter adapts v2.Logger to util.Logger
type utilLoggerAdapter struct {
	logger Logger
}

func (a *utilLoggerAdapter) Infof(format string, v ...any) {
	a.logger.Info(fmt.Sprintf(format, v...))
}

func (a *utilLoggerAdapter) Errorf(format string, v ...any) {
	a.logger.Error(fmt.Sprintf(format, v...), nil)
}

// ToInterfacesLogger adapts v2.Logger to interfaces.Logger (multi-llm-provider-go)
func ToInterfacesLogger(l Logger) interfaces.Logger {
	return &interfacesLoggerAdapter{
		logger: l,
	}
}

// interfacesLoggerAdapter adapts v2.Logger to interfaces.Logger
type interfacesLoggerAdapter struct {
	logger Logger
}

func (a *interfacesLoggerAdapter) Infof(format string, v ...any) {
	a.logger.Info(fmt.Sprintf(format, v...))
}

func (a *interfacesLoggerAdapter) Errorf(format string, v ...any) {
	a.logger.Error(fmt.Sprintf(format, v...), nil)
}

func (a *interfacesLoggerAdapter) Debugf(format string, args ...interface{}) {
	a.logger.Debug(fmt.Sprintf(format, args...))
}
