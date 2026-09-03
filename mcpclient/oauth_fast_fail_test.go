package mcpclient

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
)

// A hosted OAuth connector the user has never signed in to cannot be fixed by
// retrying: the token file only appears after an interactive login. Both retry
// loops must return after the first attempt (an agent start on a deployment
// with one such connector used to burn ~19s here before every turn).
func TestConnectFailsFastWithoutOAuthToken(t *testing.T) {
	cfg := MCPServerConfig{
		URL: "https://127.0.0.1:1/mcp", // never reached: the token check comes first
		OAuth: &oauth.OAuthConfig{
			ClientID:  "https://example.invalid/.well-known/mcp-client.json",
			AuthURL:   "https://example.invalid/authorize",
			TokenURL:  "https://example.invalid/token",
			TokenFile: filepath.Join(t.TempDir(), "Notion.json"),
		},
	}

	for _, tc := range []struct {
		name    string
		connect func(*Client, context.Context) error
	}{
		{"Connect", func(c *Client, ctx context.Context) error { return c.Connect(ctx) }},
		{"ConnectWithRetry", func(c *Client, ctx context.Context) error { return c.ConnectWithRetry(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(cfg, loggerv2.NewNoop())
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			err := tc.connect(c, ctx)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatal("expected an error without a token")
			}
			if !errors.Is(err, oauth.ErrNoValidToken) {
				t.Fatalf("error should wrap oauth.ErrNoValidToken, got: %v", err)
			}
			// The inner loop alone sleeps 1s+2s between its attempts; a single
			// attempt finishes well under that.
			if elapsed > 900*time.Millisecond {
				t.Fatalf("missing-token failure retried: took %s", elapsed)
			}
		})
	}
}
