# Remove STDIO Connection Pool Refactoring

## Goal
Remove the global STDIO connection pool. Each agent creates and owns its own connections directly.

## Why
- Pool adds complexity (mutexes, health checks, cleanup routines)
- Pool causes connection sharing between agents (race conditions)
- Creating stdio connections is cheap enough to not need pooling
- Each agent already stores connections in `a.Clients` map

## Changes Required

### 1. `mcpagent/mcpclient/stdio_manager.go`
- Remove global pool (`globalStdioPool`, `poolOnce`)
- Remove `pool` field from `StdioManager`
- Make `Connect()` create connections directly (like deprecated `CreateClient()`)
- Remove pool-related methods: `GetPoolStats()`, `CloseConnection()`, `CloseAllConnections()`, `GetGlobalPoolStats()`, `StopGlobalPool()`
- Remove deprecation warning from `CreateClient()` - it becomes the standard approach

### 2. `mcpagent/mcpclient/stdio_pool.go`
- Delete entire file (502 lines)
- All pool logic removed

### 3. `mcpagent/agent/error_handler.go`
- Update `HandleBrokenPipeError()` to:
  1. Close old connection: `h.agent.Clients[serverName].Close()`
  2. Create new connection directly (reuse connection creation logic)
  3. Update agent's map: `h.agent.Clients[serverName] = newClient`
  4. Retry tool call with new connection

### 4. `mcpagent/mcpcache/integration.go`
- `GetFreshConnection()` should create connections directly (no pool)
- `performOriginalConnectionLogic()` should use direct connection creation

### 5. Remove Pool References
- Remove pool stats/logging
- Remove pool cleanup routines
- Remove pool configuration

## Broken Pipe Recovery
- Detection: `IsBrokenPipeError()` (unchanged)
- Recovery: Agent directly replaces connection in `a.Clients[serverName]`
- No cache invalidation needed - agent owns the connection

## Benefits
- Simpler code (no mutexes, health checks, cleanup)
- No connection sharing between agents
- No deadlock risks
- Each agent fully owns its connections

