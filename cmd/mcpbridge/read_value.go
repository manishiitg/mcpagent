package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runSecretCommand reads one secret's current value from this session's server (`mcpbridge secret NAME`). The server
// decides from the chat's own secret selection and access; the value goes only to stdout, for use inline in a
// command, and is never logged. A refusal is a plain line on stderr and a non-zero exit.
func runSecretCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "usage: mcpbridge secret NAME")
		return 2
	}
	name := strings.TrimPrefix(strings.TrimSpace(args[0]), "SECRET_")
	apiURL := strings.TrimRight(strings.TrimSpace(os.Getenv("MCP_API_URL")), "/")
	token := strings.TrimSpace(os.Getenv("VAR_AGENTWORKS_SECRET_READ_TOKEN"))
	session := strings.TrimSpace(os.Getenv("MCP_SESSION_ID"))
	if apiURL == "" || token == "" || session == "" {
		fmt.Fprintln(stderr, "mcpbridge secret: this shell is not connected to a chat session")
		return 1
	}
	body, _ := json.Marshal(map[string]string{"name": name})
	// The read has its own route (it takes only the shell's secret-read token); a session-scoped base drops its /s/<id>.
	if base, _, scoped := strings.Cut(apiURL, "/s/"); scoped {
		apiURL = base
	}
	req, err := http.NewRequest(http.MethodPost, apiURL+"/bridge/secret-read", strings.NewReader(string(body))) // #nosec G107 -- MCP_API_URL is set by the launcher for this session
	if err != nil {
		fmt.Fprintln(stderr, "mcpbridge secret: "+err.Error())
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "mcpbridge secret: cannot reach the server")
		return 1
	}
	defer resp.Body.Close()
	var out struct {
		Value string `json:"value"`
		Error string `json:"error"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if decodeErr != nil || resp.StatusCode != http.StatusOK {
		message := strings.TrimSpace(out.Error)
		if message == "" {
			message = fmt.Sprintf("the server refused (%d)", resp.StatusCode)
		}
		fmt.Fprintln(stderr, "mcpbridge secret: "+message)
		return 1
	}
	fmt.Fprint(stdout, out.Value)
	return 0
}
