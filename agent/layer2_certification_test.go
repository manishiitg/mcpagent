package mcpagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLayer2P0RegistryConsistent is the drift guard on the registry itself:
// every entry must be well-formed and unique. Mirrors Layer 1's contract<->
// registry agreement test — the registry can never silently misrepresent what
// Layer 2 requires.
func TestLayer2P0RegistryConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Layer2P0Certifications {
		if c.ID == "" {
			t.Errorf("cert with empty ID (test %q)", c.TestName)
		}
		if c.TestName == "" {
			t.Errorf("cert %q has no test name", c.ID)
		}
		if len(c.Providers) == 0 {
			t.Errorf("cert %q lists no providers", c.ID)
		}
		if c.Transport != Layer2TransportTmux && c.Transport != Layer2TransportJSON {
			t.Errorf("cert %q has invalid transport %q", c.ID, c.Transport)
		}
		if seen[c.ID] {
			t.Errorf("duplicate cert ID %q", c.ID)
		}
		seen[c.ID] = true
		for _, p := range c.Providers {
			switch p {
			case "Claude", "Codex", "Cursor", "Pi":
			default:
				t.Errorf("cert %q lists unknown provider %q", c.ID, p)
			}
		}
	}
}

// TestLayer2P0AgentReviewedEvidence is the enforcement gate: every agent-reviewed
// P0 capability must keep an APPROVED agentreview record on disk for each of its
// required providers. It reads only committed testdata (no CLI, always runs), so
// CI fails the moment a required Layer-2 capability loses its reviewed evidence
// — the requiredP0CertificationIDs analogue Layer 2 previously lacked.
//
// It deliberately does NOT re-check reviewed_fingerprint against the record's
// current fingerprint: that stronger check (has the reviewed output drifted?)
// belongs to the live e2e via agentreview.RequireReviewed. Here the invariant is
// simply "an approved record exists", which is what regresses when a test or its
// evidence is deleted.
func TestLayer2P0AgentReviewedEvidence(t *testing.T) {
	const dir = "testdata/agent-reviews"
	for _, c := range Layer2P0Certifications {
		if !c.AgentReviewed {
			continue
		}
		for _, p := range c.Providers {
			path := filepath.Join(dir, c.TestName+"_"+p+".json")
			// #nosec G304 - path is built from the in-repo registry, not user input.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("P0 %s (%s): missing agentreview evidence for %s: %v", c.ID, c.Transport, p, err)
				continue
			}
			var rec struct {
				Review struct {
					Verdict             string `json:"verdict"`
					ReviewedFingerprint string `json:"reviewed_fingerprint"`
				} `json:"agent_review"`
			}
			if err := json.Unmarshal(data, &rec); err != nil {
				t.Errorf("P0 %s (%s/%s): unreadable agentreview record %s: %v", c.ID, c.Transport, p, path, err)
				continue
			}
			if rec.Review.Verdict != "good" {
				t.Errorf("P0 %s (%s/%s): agentreview not approved (verdict=%q) at %s", c.ID, c.Transport, p, rec.Review.Verdict, path)
			}
			if rec.Review.ReviewedFingerprint == "" {
				t.Errorf("P0 %s (%s/%s): agentreview record has empty reviewed_fingerprint at %s", c.ID, c.Transport, p, path)
			}
		}
	}
}
