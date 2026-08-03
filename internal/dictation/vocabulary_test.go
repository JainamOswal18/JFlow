package dictation

import (
	"path/filepath"
	"testing"
)

func TestVocabularyApplyUsesWholePhrases(t *testing.T) {
	s := NewVocabularyStore(filepath.Join(t.TempDir(), "vocabulary.json"))
	if _, err := s.Add("j flow", "JFlow"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("hyper land", "Hyprland"); err != nil {
		t.Fatal(err)
	}
	got := s.Apply("Open J flow, then hyper land. workflow should stay unchanged.")
	want := "Open JFlow, then Hyprland. workflow should stay unchanged."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVocabularyDelete(t *testing.T) {
	s := NewVocabularyStore(filepath.Join(t.TempDir(), "vocabulary.json"))
	entry, err := s.Add("pipe wire", "PipeWire")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(entry.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want none", len(entries))
	}
}
