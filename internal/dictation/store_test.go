package dictation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	j := &Job{ID: "test", Status: StatusQueued, CreatedAt: time.Now(), AudioPath: filepath.Join(dir, "test", "audio.wav")}
	if err := s.Save(j); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusQueued || got.ID != "test" {
		t.Fatalf("unexpected job: %#v", got)
	}
}

func TestWavWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.wav")
	w, err := newWavWriter(path, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{0, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 48 {
		t.Fatalf("got %d-byte wav, want 48", info.Size())
	}
}

func TestRepairWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted.wav")
	if err := os.WriteFile(path, append(make([]byte, 44), []byte{1, 0, 2, 0}...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repairWAV(path, 16000); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatal("repair did not write a WAV header")
	}
}
