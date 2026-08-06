package dictation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypeTextAllowsLongVirtualKeyboardStream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wtype")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if err := typeText(strings.Repeat("long dictation ", 80)); err != nil {
		t.Fatalf("long virtual-keyboard stream timed out: %v", err)
	}
}
