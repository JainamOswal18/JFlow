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
	if _, err := s.Add("c plus plus", "C++"); err != nil {
		t.Fatal(err)
	}
	got := s.Apply("Open J flow, then hyper land. hyper land hyper land. c plus plus is not c plus pluser. workflow should stay unchanged.")
	want := "Open JFlow, then Hyprland. Hyprland Hyprland. C++ is not c plus pluser. workflow should stay unchanged."
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

func TestVocabularyCanonicalTermAndLearnedAlias(t *testing.T) {
	s := NewVocabularyStore(filepath.Join(t.TempDir(), "vocabulary.json"))
	entry, err := s.AddCanonical("Jainam Oswal")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Canonical != "Jainam Oswal" || len(entry.Aliases) != 0 {
		t.Fatalf("unexpected entry: %#v", entry)
	}
	if _, err := s.LearnAlias("Jainam Oswal", "Jay Nam Oswal"); err != nil {
		t.Fatal(err)
	}
	got := s.Apply("Hi, I'm jay nam oswal. jainam oswal is here.")
	want := "Hi, I'm Jainam Oswal. Jainam Oswal is here."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	terms, err := s.Keyterms(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 || terms[0] != "Jainam Oswal" {
		t.Fatalf("got keyterms %#v", terms)
	}
}

func TestVocabularyLearnFromCorrectionFindsSpacingVariant(t *testing.T) {
	s := NewVocabularyStore(filepath.Join(t.TempDir(), "vocabulary.json"))
	learned, err := s.LearnFromCorrection("Hi, I'm Jay Nam Oswal", "Hi, I'm Jainam Oswal")
	if err != nil {
		t.Fatal(err)
	}
	if learned == 0 {
		t.Fatal("expected at least one learned alias")
	}
	got := s.Apply("JAY NAM OSWAL")
	if got != "Jainam Oswal" {
		t.Fatalf("got %q", got)
	}
}
