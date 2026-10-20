package mcpclient

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
)

// statusLoggingTransport records an MCP server's error answer (status, WWW-Authenticate and the start of the body).
// The MCP transport turns any 4xx on initialize into one generic "legacy SSE server" error, which hid why a hosted
// server refused the connection. Request headers (tokens) are never logged.
type statusLoggingTransport struct{ base http.RoundTripper }

func (t statusLoggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil || resp == nil || resp.StatusCode < 400 {
		return resp, err
	}
	var excerpt []byte
	if resp.Body != nil {
		excerpt, _ = io.ReadAll(io.LimitReader(resp.Body, 512))
		rest := resp.Body
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(excerpt), rest), rest}
	}
	body := strings.Join(strings.Fields(string(excerpt)), " ")
	if len(body) > 300 {
		body = body[:300]
	}
	log.Printf("[MCP_HTTP] %s %s%s returned %d www-authenticate=%q body=%q", r.Method, r.URL.Host, r.URL.Path, resp.StatusCode, resp.Header.Get("WWW-Authenticate"), body)
	return resp, nil
}

// withStatusLogging returns a copy of client (or a default one) whose transport logs error answers.
func withStatusLogging(client *http.Client) *http.Client {
	out := &http.Client{}
	if client != nil {
		copied := *client
		out = &copied
	}
	base := out.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out.Transport = statusLoggingTransport{base: base}
	return out
}
