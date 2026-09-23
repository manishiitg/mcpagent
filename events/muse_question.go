package events

import "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"

// MuseQuestionEvent is the native prompt lifecycle projected into chat.
type MuseQuestionEvent struct {
	BaseEventData
	Provider        string                   `json:"provider"`
	NativeSessionID string                   `json:"native_session_id"`
	RunID           string                   `json:"run_id"`
	NativeSequence  int64                    `json:"native_sequence"`
	PromptID        string                   `json:"prompt_id"`
	Kind            string                   `json:"kind"`
	Questions       []musecli.Question       `json:"questions,omitempty"`
	Answers         []musecli.QuestionAnswer `json:"answers,omitempty"`
	Outcome         string                   `json:"outcome,omitempty"`
}

func (*MuseQuestionEvent) GetEventType() EventType { return CodingAgentQuestion }
