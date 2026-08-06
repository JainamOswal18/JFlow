package dictation

import "testing"

func TestInferContextHintUsesSmallSafeVocabulary(t *testing.T) {
	tests := []struct {
		target WindowTarget
		want   string
	}{
		{WindowTarget{Class: "brave", Title: "ChatGPT - new chat"}, "AI-assistant request"},
		{WindowTarget{Class: "brave-browser", Title: "Create a post | LinkedIn"}, "LinkedIn post"},
		{WindowTarget{Class: "thunderbird", Title: "Compose"}, "professional message"},
		{WindowTarget{Class: "discord", Title: "general"}, "casual message"},
		{WindowTarget{Class: "brave", Title: "Some private document"}, ""},
	}
	for _, test := range tests {
		if got := InferContextHint(test.target); test.want == "" && got != "" || test.want != "" && !containsAny(got, test.want) {
			t.Errorf("InferContextHint(%+v) = %q, want %q", test.target, got, test.want)
		}
	}
}

func TestHasVoice(t *testing.T) {
	silence := make([]byte, 640)
	if hasVoice(silence, 100) {
		t.Fatal("silence was detected as voice")
	}
	voice := make([]byte, 640)
	for i := 0; i < len(voice); i += 2 {
		voice[i] = 0xdc // little-endian 1500
		voice[i+1] = 0x05
	}
	if !hasVoice(voice, 650) {
		t.Fatal("voice was not detected")
	}
}
