package grpcserver

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"
)

func TestManagedAgentIDsUseFullUUIDEntropy(t *testing.T) {
	uuidID := regexp.MustCompile(`^agent_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sessionUUIDID := regexp.MustCompile(`^session_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	if got := newManagedAgentID(); !uuidID.MatchString(got) {
		t.Fatalf("agent id = %q, want full UUID-shaped id", got)
	}
	if got := newManagedSessionID(); !sessionUUIDID.MatchString(got) {
		t.Fatalf("session id = %q, want full UUID-shaped id", got)
	}
	if a, b := newManagedAgentID(), newManagedAgentID(); a == b {
		t.Fatalf("generated duplicate agent ids: %q", a)
	}
}

func TestServerStartRestrictsUnixSocketPermissions(t *testing.T) {
	tmp, err := os.CreateTemp("/tmp", "mcpagent-grpc-*.sock")
	if err != nil {
		t.Fatalf("create temp socket path: %v", err)
	}
	socketPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	server := NewServer(Config{SocketPath: socketPath})
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	var info os.FileInfo
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("server exited before socket was ready: %v", err)
		default:
		}
		var err error
		info, err = os.Stat(socketPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if info == nil {
		t.Fatal("timed out waiting for Unix socket")
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %#o, want 0600", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("server did not stop after Shutdown")
	}
}
