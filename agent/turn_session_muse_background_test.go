package mcpagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMuseBackgroundWatcherEmitsAfterForegroundTerminal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	const nativeID = "native-background-test"
	path := filepath.Join(root, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	appendRow := func(sequence int, scope, kind string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // path is under this test's temporary directory.
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, `{"sequence":%d,"payload_type":"runtime.session","payload":{"kind":%q,"run_id":"run-1","event":{"kind":%q,"task_id":"task-1"}}}`+"\n", sequence, scope, kind)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendRow(1, "run", "task_backgrounded")
	appendRow(2, "run", "terminal")
	tracer := NewStreamingTracer(observability.NoopTracer{}, 16)
	agent := &Agent{sessionID: "owner-background-test", logger: loggerv2.NewDefault(), tracers: []observability.Tracer{tracer}}
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ch, unsubscribe := tracer.SubscribeToEvents(context.Background())
	defer unsubscribe()
	session.startMuseBackgroundWatcher(nativeID, 0)
	select {
	case event := <-ch:
		if event.Type != events.CodingAgentBackgroundTask {
			t.Fatalf("first event=%s", event.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background marker not emitted")
	}
	appendRow(3, "task", "completed")
	select {
	case event := <-ch:
		data, ok := event.Data.(*events.CodingAgentBackgroundTaskEvent)
		if !ok || data.Kind != "completed" || data.NativeSequence != 3 {
			t.Fatalf("late event=%+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late completion not emitted")
	}
}

func TestMuseRestoredSessionReplaysNativeTaskRows(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	const nativeID = "restored-native-background"
	path := filepath.Join(root, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	rows := `{"sequence":1,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"task_backgrounded","task_id":"task-1"}}}` + "\n" +
		`{"sequence":2,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"terminal","terminal":"completed"}}}` + "\n" +
		`{"sequence":3,"payload_type":"runtime.session","payload":{"kind":"task","run_id":"run-1","event":{"kind":"completed","task_id":"task-1"}}}` + "\n"
	if err := os.WriteFile(path, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	tracer := NewStreamingTracer(observability.NoopTracer{}, 16)
	agent := &Agent{sessionID: "restored-owner", provider: llm.ProviderMuseCLI, logger: loggerv2.NewDefault(), tracers: []observability.Tracer{tracer},
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", NativeSessionID: nativeID}}
	ch, unsubscribe := tracer.SubscribeToEvents(context.Background())
	defer unsubscribe()
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	kinds := make([]string, 0, 2)
	for len(kinds) < 2 {
		select {
		case event := <-ch:
			if event.Type == events.CodingAgentBackgroundTask {
				kinds = append(kinds, event.Data.(*events.CodingAgentBackgroundTaskEvent).Kind)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("restored kinds=%v", kinds)
		}
	}
	if kinds[0] != "task_backgrounded" || kinds[1] != "completed" {
		t.Fatalf("restored kinds=%v", kinds)
	}
}
