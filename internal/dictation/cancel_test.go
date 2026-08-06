package dictation

import (
	"context"
	"testing"
	"time"
)

func TestCancelIfRecordingLeavesDeliveryRunning(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.processingCancel = cancel

	if err := d.CancelIfRecording(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("Escape cancellation stopped an already-recorded dictation during delivery")
	case <-time.After(20 * time.Millisecond):
	}
}
