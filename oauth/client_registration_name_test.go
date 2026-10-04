package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The name a remote server's consent screen shows for the app asking for access.
func TestRegisterClientSendsTheAppName(t *testing.T) {
	var got ClientRegistrationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode registration request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"c1"}`))
	}))
	defer server.Close()

	if _, err := (Discoverer{}).RegisterClient(server.URL, "http://localhost/callback"); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if got.ClientName != "AgentWorks" {
		t.Errorf("client_name = %q, want AgentWorks", got.ClientName)
	}
	if got.ClientURI != "https://agentworkshq.com" {
		t.Errorf("client_uri = %q, want the AgentWorks site", got.ClientURI)
	}
}
