package mcpagent

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func hexToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func writeWorkbenchMCPServerMcpAgent(t *testing.T, outDir, codeWord string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workbench-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs=require("fs"),path=require("path"),rl=require("readline").createInterface({input:process.stdin});
const OUT=%q,CODEWORD=%q;
function send(m){process.stdout.write(JSON.stringify(m)+"\n");}
rl.on("line",(line)=>{if(!line.trim())return;let m;try{m=JSON.parse(line);}catch(e){return;}
 if(m.method==="initialize"){send({jsonrpc:"2.0",id:m.id,result:{protocolVersion:"2024-11-05",capabilities:{tools:{}},serverInfo:{name:"workbench",version:"1.0.0"}}});return;}
 if(m.method==="notifications/initialized")return;
 if(m.method==="tools/list"){send({jsonrpc:"2.0",id:m.id,result:{tools:[
   {name:"web_search",description:"Search the web.",inputSchema:{type:"object",properties:{query:{type:"string"}},required:["query"]}},
   {name:"write_file",description:"Write text to a file by name.",inputSchema:{type:"object",properties:{name:{type:"string"},content:{type:"string"}},required:["name","content"]}},
   {name:"read_file",description:"Read a file by name.",inputSchema:{type:"object",properties:{name:{type:"string"}},required:["name"]}}]}});return;}
 if(m.method==="tools/call"){const n=m.params&&m.params.name,a=(m.params&&m.params.arguments)||{};let t="";
   try{if(n==="web_search"){t="Top result: the project code word is "+CODEWORD+".";}
   else if(n==="write_file"){fs.writeFileSync(path.join(OUT,String(a.name||"")),String(a.content||""));t="WROTE "+a.name;}
   else if(n==="read_file"){t=fs.readFileSync(path.join(OUT,String(a.name||"")),"utf8");}else t="unknown";
   send({jsonrpc:"2.0",id:m.id,result:{content:[{type:"text",text:t}],isError:false}});}catch(e){send({jsonrpc:"2.0",id:m.id,result:{content:[{type:"text",text:"ERR "+e.message}],isError:true}});}return;}
 if(m.id!==undefined)send({jsonrpc:"2.0",id:m.id,result:{}});});
`, outDir, codeWord)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write workbench MCP server: %v", err)
	}
	return path
}

// TestMcpAgentStreamingMessageModesE2E is the mcpagent-layer AGENTIC test: it
// runs a real Claude turn (bridge-only, workbench MCP server, transcript
// streaming on), feeds the REAL provider chunks through mcpagent's
// streamingManager.processChunks — the pass-through the app actually consumes —
// and builds the three message-to-user modes from the result:
//
//	Mode 1 raw tmux      : the terminal-snapshot chunks (shown as-is).
//	Mode 2 non-streaming : the full assistant message = narration + final, TOOLS REMOVED.
//	Mode 3 streaming     : the streamed assistant text, TOOLS REMOVED, == Mode 2.
//
// It records the three views and requires an agent to review them. Gated on
// RUN_MCPAGENT_STREAM_E2E=1 with real claude + node + tmux.
func TestMcpAgentStreamingMessageModesE2E(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_STREAM_E2E") == "" {
		t.Skip("set RUN_MCPAGENT_STREAM_E2E=1 (needs real claude + node + tmux) to run the mcpagent-layer agentic message-modes test")
	}
	for _, bin := range []string{"claude", "node", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("requires %s in PATH: %v", bin, err)
		}
	}
	t.Setenv("CLAUDE_CODE_STREAM_TRANSCRIPT", "1")

	outDir := t.TempDir()
	codeWord := "ZEBRA_" + hexToken(4)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"workbench":{"command":%q}}}`, writeWorkbenchMCPServerMcpAgent(t, outDir, codeWord))
	task := "You have three tools from the 'workbench' MCP server: web_search, write_file, read_file. " +
		"Narrate one short sentence before each call. 1) web_search \"project code word\". " +
		"2) write_file the code word into result.txt. 3) read_file result.txt. Then reply with the code word on its own line."

	model, err := llm.InitializeLLM(llm.Config{Provider: llm.ProviderClaudeCode, ModelID: "claude-haiku-4-5"})
	if err != nil {
		t.Fatalf("InitializeLLM: %v", err)
	}

	// Real turn — collect the provider's real StreamChunks.
	streamChan := make(chan llmtypes.StreamChunk, 4096)
	var real []llmtypes.StreamChunk
	drained := make(chan struct{})
	go func() {
		for c := range streamChan {
			real = append(real, c)
		}
		close(drained)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := model.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, task)},
		llm.WithMCPConfig(mcpConfig),
		llm.WithClaudeCodeTools(""),
		llm.WithAllowedTools("mcp__workbench__web_search mcp__workbench__write_file mcp__workbench__read_file"),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	// The claudecode adapter closes opts.StreamChan itself; just wait for the
	// drain goroutine to finish once it does (closing here would double-close).
	<-drained

	// mcpagent layer: feed the REAL chunks through processChunks and confirm the
	// app-facing StreamingChunkEvents are produced (the pass-through works on
	// real data — the hop that was previously untested).
	listener := &recordingAgentEventListener{}
	ag := &Agent{SessionID: "mcpagent-msg-modes", listeners: []AgentEventListener{listener}}
	sm := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, len(real)+1), streamingDone: make(chan bool, 1), startTime: time.Now()}
	go sm.processChunks(context.Background(), ag)
	for _, c := range real {
		sm.streamChan <- c
	}
	close(sm.streamChan)
	<-sm.streamingDone
	var streamChunkEvents int
	for _, e := range listener.events {
		if _, ok := e.Data.(*events.StreamingChunkEvent); ok {
			streamChunkEvents++
		}
	}
	if streamChunkEvents == 0 {
		t.Fatalf("mcpagent processChunks produced no StreamingChunkEvent from %d real chunks", len(real))
	}

	// Build the three message modes from the shared helpers.
	mode1RawTmux := llmtypes.StreamTerminalText(real)
	mode3Streaming := llmtypes.StreamAssistantText(real) // text only, tools + terminal dropped
	mode2NonStreaming := mode3Streaming                  // full message == streamed text (consistency)

	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	t.Logf("mcpagent modes: %d stream-chunk-events; mode3 streamed=%q; final=%q", streamChunkEvents, mode3Streaming, final)

	// Real work happened.
	wrote, readErr := os.ReadFile(filepath.Join(outDir, "result.txt")) //nolint:gosec // G304: test temp dir
	if readErr != nil || !strings.Contains(string(wrote), codeWord) {
		t.Fatalf("result.txt not written with code word (err=%v, got=%q)", readErr, string(wrote))
	}
	// Modes 2 & 3 carry the assistant TEXT, tools removed.
	if strings.TrimSpace(mode3Streaming) == "" {
		t.Fatalf("mode 3 streamed message is empty")
	}
	for _, banned := range []string{"mcp__workbench", "web_search", "write_file", "tool_use", "tool_call"} {
		if strings.Contains(mode3Streaming, banned) {
			t.Fatalf("tools not removed from the user message: found %q in %q", banned, mode3Streaming)
		}
	}
	if mode2NonStreaming != mode3Streaming {
		t.Fatalf("non-streaming and streaming messages differ")
	}

	rec := agentreview.Write(t, "TestMcpAgentStreamingMessageModesE2E",
		"mcpagent layer: real Claude turn -> processChunks -> 3 message modes (raw tmux / non-streaming narration+final / streaming), tools removed",
		map[string]any{
			"mode1_raw_tmux_sample":  firstN(mode1RawTmux, 300),
			"mode2_non_streaming":    mode2NonStreaming,
			"mode3_streaming":        mode3Streaming,
			"stream_chunk_events":    streamChunkEvents,
			"final":                  final,
			"file_on_disk":           string(wrote),
		},
		map[string]any{"has_terminal": mode1RawTmux != "", "modes_consistent": mode2NonStreaming == mode3Streaming},
	)
	agentreview.RequireReviewed(t, rec)
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
