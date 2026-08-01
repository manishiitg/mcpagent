package mcpagent

import (
	"reflect"
	"slices"
	"testing"
)

// This is the completed migration ratchet for the Agent surface. It started at
// 70 methods and now pins the final Agent/Session API exactly. Any deliberate
// change updates this list in the same commit.
func TestAgentPublicMethodSurface(t *testing.T) {
	want := []string{"Close", "Definition", "Run", "Start"}

	typeOfAgent := reflect.TypeOf((*Agent)(nil))
	got := make([]string, 0, typeOfAgent.NumMethod())
	for i := 0; i < typeOfAgent.NumMethod(); i++ {
		got = append(got, typeOfAgent.Method(i).Name)
	}

	if len(got) != 4 {
		t.Fatalf("exported *Agent method count = %d, want final surface 4; methods=%v", len(got), got)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Agent methods changed\n got: %v\nwant: %v", got, want)
	}
}

func TestSessionPublicMethodSurface(t *testing.T) {
	want := []string{"Close", "Events", "Run", "Send", "Snapshot"}
	typeOfSession := reflect.TypeOf((*Session)(nil))
	got := make([]string, 0, typeOfSession.NumMethod())
	for i := 0; i < typeOfSession.NumMethod(); i++ {
		got = append(got, typeOfSession.Method(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Session methods changed\n got: %v\nwant: %v", got, want)
	}
}
