package mcpagent

import "testing"

func TestActualMCPToolName(t *testing.T) {
	tests := []struct {
		name        string
		exposedName string
		serverName  string
		want        string
	}{
		{
			name:        "qualified duplicate",
			exposedName: "github__search",
			serverName:  "github",
			want:        "search",
		},
		{
			name:        "ordinary tool",
			exposedName: "search",
			serverName:  "github",
			want:        "search",
		},
		{
			name:        "legitimate double underscore",
			exposedName: "query__records",
			serverName:  "database",
			want:        "query__records",
		},
		{
			name:        "qualified tool containing double underscore",
			exposedName: "database__query__records",
			serverName:  "database",
			want:        "query__records",
		},
		{
			name:        "different server prefix",
			exposedName: "gitlab__search",
			serverName:  "github",
			want:        "gitlab__search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := actualMCPToolName(tt.exposedName, tt.serverName); got != tt.want {
				t.Fatalf("actualMCPToolName(%q, %q) = %q, want %q", tt.exposedName, tt.serverName, got, tt.want)
			}
		})
	}
}
