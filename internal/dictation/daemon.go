package dictation

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Daemon struct {
	cfg       Config
	store     *Store
	mu        sync.Mutex
	recording *recording
	phase     Status
	work      chan string
}

type recording struct {
	jobID    string
	cmd      *exec.Cmd
	done     chan error
	realtime *RealtimeScribe
	wav      *wavWriter
}

func NewDaemon(cfg Config) (*Daemon, error) {
	for _, dir := range []string{cfg.DataDir, cfg.StateDir, cfg.RuntimeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	store, err := NewStore(cfg.JobsDir())
	if err != nil {
		return nil, err
	}
	d := &Daemon{cfg: cfg, store: store, phase: Status{Phase: "idle"}, work: make(chan string, 32)}
	d.recoverInterruptedJobs()
	return d, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := os.Remove(d.cfg.SocketPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", d.cfg.SocketPath())
	if err != nil {
		return err
	}
	if err := os.Chmod(d.cfg.SocketPath(), 0600); err != nil {
		_ = ln.Close()
		return err
	}
	defer func() { _ = ln.Close(); _ = os.Remove(d.cfg.SocketPath()) }()
	go func() {
		<-ctx.Done()
		_ = ln.Close() // unblock Accept so systemd restarts do not hang
	}()
	d.setStatus("idle", "Ready")
	go d.worker(ctx)
	go d.recoveryLoop(ctx)
	go d.cleanupLoop(ctx)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go d.handleConnection(conn)
	}
}

type Command struct {
	Action string `json:"action"`
	JobID  string `json:"job_id,omitempty"`
}
type Response struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Status Status `json:"status"`
	Jobs   []*Job `json:"jobs,omitempty"`
}

func SendCommand(socket string, cmd Command) (Response, error) {
	var resp Response
	c, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return resp, fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()
	// Hotkeys should fail visibly rather than leave a background shell process
	// waiting forever if the daemon is unhealthy or a provider is slow to start.
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(c).Encode(cmd); err != nil {
		return resp, err
	}
	if err := json.NewDecoder(io.LimitReader(c, 2<<20)).Decode(&resp); err != nil {
		return resp, err
	}
	return resp, nil
}

func (d *Daemon) handleConnection(c net.Conn) {
	defer c.Close()
	var cmd Command
	err := json.NewDecoder(io.LimitReader(c, 64<<10)).Decode(&cmd)
	resp := Response{Status: d.Status()}
	if err != nil {
		resp.Error = err.Error()
	} else {
		switch cmd.Action {
		case "start":
			err = d.Start()
		case "stop":
			err = d.Stop()
		case "toggle":
			if d.isRecording() {
				err = d.Stop()
			} else {
				err = d.Start()
			}
		case "cancel":
			err = d.Cancel()
		case "retry-last":
			err = d.RetryLast()
		case "dismiss-last":
			err = d.DismissLast()
		case "retry":
			err = d.Retry(cmd.JobID)
		case "status":
		case "history":
			resp.Jobs, err = d.store.List()
		default:
			err = fmt.Errorf("unknown action %q", cmd.Action)
		}
		resp.Status = d.Status()
	}
	resp.OK = err == nil
	if err != nil {
		resp.Error = err.Error()
	}
	_ = json.NewEncoder(c).Encode(resp)
}

func (d *Daemon) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recording != nil {
		return errors.New("already recording")
	}
	id, err := newID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	job := &Job{ID: id, Status: StatusRecording, CreatedAt: now, AudioPath: d.store.AudioPath(id), Target: activeWindow()}
	if err := d.store.Save(job); err != nil {
		return err
	}
	r, err := d.beginRecording(job)
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		_ = d.store.Save(job)
		return err
	}
	d.recording = r
	d.setStatusLocked("recording", "Listening")
	return nil
}

func (d *Daemon) beginRecording(job *Job) (*recording, error) {
	args := []string{"--raw", "--rate", fmt.Sprint(d.cfg.SampleRate), "--channels", "1", "--format", "s16", "-"}
	if d.cfg.MicTarget != "" {
		args = append(args[:1], append([]string{"--target", d.cfg.MicTarget}, args[1:]...)...)
	}
	cmd := exec.Command("pw-record", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pw-record: %w", err)
	}
	r := &recording{jobID: job.ID, cmd: cmd, done: make(chan error, 1)}
	wav, err := newWavWriter(job.AudioPath, d.cfg.SampleRate)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	r.wav = wav
	if d.cfg.ASR.Provider == "elevenlabs_realtime" {
		streamCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		r.realtime, _ = StartRealtimeScribe(streamCtx, d.cfg)
		cancel()
	}
	go func() {
		buf := make([]byte, 6400) // 200 ms of 16 kHz mono PCM
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				if _, err := wav.Write(chunk); err != nil {
					r.done <- err
					return
				}
				if r.realtime != nil {
					_ = r.realtime.Send(context.Background(), chunk)
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					r.done <- readErr
				} else {
					r.done <- nil
				}
				return
			}
		}
	}()
	return r, nil
}

func (d *Daemon) Stop() error {
	d.mu.Lock()
	r := d.recording
	if r == nil {
		d.mu.Unlock()
		return errors.New("not recording")
	}
	d.recording = nil
	d.setStatusLocked("processing", "Finalizing audio")
	d.mu.Unlock()
	if err := r.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	// pw-record exits with status 1 on SIGINT on this PipeWire build. We sent
	// that signal deliberately to finalize the recording, so the WAV/pipe result
	// (not the process exit code) determines whether capture succeeded.
	_ = r.cmd.Wait()
	pipeErr := <-r.done
	if err := r.wav.Close(); err != nil {
		pipeErr = err
	}
	if r.realtime != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		text, err := r.realtime.Commit(ctx)
		cancel()
		r.realtime.Close()
		if err == nil && strings.TrimSpace(text) != "" {
			job, getErr := d.store.Get(r.jobID)
			if getErr == nil {
				job.Transcript = text
				_ = d.store.Save(job)
			}
		}
	}
	job, err := d.store.Get(r.jobID)
	if err != nil {
		return err
	}
	if info, err := os.Stat(job.AudioPath); err != nil || info.Size() < 3200 {
		job.Status = StatusCancelled
		job.Error = "No speech captured (very short recording)"
		_ = d.store.Save(job)
		_ = os.Remove(job.AudioPath)
		d.setStatus("idle", "Ready")
		return nil
	}
	if pipeErr != nil {
		job.Status = StatusFailed
		job.Error = pipeErr.Error()
		_ = d.store.Save(job)
		d.setStatus("error", "Microphone capture failed")
		return pipeErr
	}
	job.Status = StatusQueued
	job.Error = ""
	_ = d.store.Save(job)
	d.enqueue(job.ID)
	d.setStatus("processing", "Transcribing")
	return nil
}

func isInterrupt(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && (exit.ProcessState.ExitCode() == -1 || strings.Contains(strings.ToLower(err.Error()), "interrupt"))
}
func (d *Daemon) Cancel() error {
	if !d.isRecording() {
		return errors.New("not recording")
	}
	d.mu.Lock()
	r := d.recording
	d.recording = nil
	d.mu.Unlock()
	_ = r.cmd.Process.Kill()
	_ = r.cmd.Wait()
	<-r.done
	_ = r.wav.Close()
	if r.realtime != nil {
		r.realtime.Close()
	}
	job, err := d.store.Get(r.jobID)
	if err == nil {
		job.Status = StatusCancelled
		job.Error = "Cancelled"
		_ = d.store.Save(job)
		_ = os.Remove(job.AudioPath)
	}
	d.setStatus("idle", "Ready")
	return nil
}
func (d *Daemon) Retry(id string) error {
	if d.isRecording() {
		return errors.New("cannot retry while dictating")
	}
	job, err := d.store.Get(id)
	if err != nil {
		return err
	}
	if job.Status == StatusDelivered {
		return errors.New("job was already delivered")
	}
	if _, err := os.Stat(job.AudioPath); err != nil {
		return fmt.Errorf("saved audio is unavailable: %w", err)
	}
	if err := repairWAV(job.AudioPath, d.cfg.SampleRate); err != nil {
		return fmt.Errorf("repair saved audio: %w", err)
	}
	job.Status = StatusQueued
	job.Error = ""
	job.NextAttemptAt = time.Time{}
	job.DeliveryAttempted = false
	if err := d.store.Save(job); err != nil {
		return err
	}
	d.enqueue(id)
	return nil
}
func (d *Daemon) RetryLast() error {
	jobs, err := d.store.List()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Status == StatusFailed || j.Status == StatusRetryWait {
			return d.Retry(j.ID)
		}
	}
	return errors.New("no failed dictation to retry")
}
func (d *Daemon) DismissLast() error {
	if d.isRecording() {
		return errors.New("cannot dismiss while dictating")
	}
	jobs, err := d.store.List()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Status == StatusFailed || j.Status == StatusRetryWait {
			j.Status = StatusCancelled
			j.Error = "Dismissed by user"
			if err := d.store.Save(j); err != nil {
				return err
			}
			d.setStatus("idle", "Ready")
			return nil
		}
	}
	return errors.New("no failed dictation to dismiss")
}
func (d *Daemon) isRecording() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.recording != nil }
func (d *Daemon) enqueue(id string) {
	select {
	case d.work <- id:
	default:
		go func() { d.work <- id }()
	}
}

func (d *Daemon) worker(ctx context.Context) {
	for {
		select {
		case id := <-d.work:
			d.process(ctx, id)
		case <-ctx.Done():
			return
		}
	}
}
func (d *Daemon) recoveryLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			jobs, _ := d.store.DueJobs(time.Now())
			for _, j := range jobs {
				d.enqueue(j.ID)
			}
		case <-ctx.Done():
			return
		}
	}
}
func (d *Daemon) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(d.cfg.CleanupInterval())
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.cleanup()
		case <-ctx.Done():
			return
		}
	}
}
func (d *Daemon) cleanup() {
	jobs, err := d.store.List()
	if err != nil {
		return
	}
	for _, j := range jobs {
		age := time.Since(j.UpdatedAt)
		if j.Status == StatusDelivered && age > time.Hour {
			_ = os.Remove(j.AudioPath)
		}
		if (j.Status == StatusFailed || j.Status == StatusRetryWait) && age > time.Duration(d.cfg.FailedRetention)*24*time.Hour {
			_ = os.RemoveAll(d.store.jobDir(j.ID))
			continue
		}
		if j.Status == StatusDelivered && age > time.Duration(d.cfg.HistoryRetention)*24*time.Hour {
			_ = os.RemoveAll(d.store.jobDir(j.ID))
		}
	}
}

// Preserve retryable audio across service restarts, while never risking a
// duplicate insertion after a crash in the tiny window around wtype.
func (d *Daemon) recoverInterruptedJobs() {
	jobs, err := d.store.List()
	if err != nil {
		return
	}
	for _, job := range jobs {
		switch job.Status {
		case StatusRecording:
			if info, err := os.Stat(job.AudioPath); err == nil && info.Size() > 44 {
				_ = repairWAV(job.AudioPath, d.cfg.SampleRate)
				job.Status = StatusQueued
				job.Error = "Recovered recording after service restart"
				_ = d.store.Save(job)
				d.enqueue(job.ID)
			} else {
				job.Status = StatusFailed
				job.Error = "Recording interrupted before audio was captured"
				_ = d.store.Save(job)
			}
		case StatusTranscribing, StatusCleaning:
			job.Status = StatusQueued
			job.Error = "Recovered after service restart"
			_ = d.store.Save(job)
			d.enqueue(job.ID)
		case StatusDelivering:
			if job.DeliveryAttempted {
				job.Status = StatusFailed
				job.ClipboardBackup = true
				job.Error = "Insertion interrupted; final text retained to avoid duplicate typing"
				_ = d.store.Save(job)
			} else {
				job.Status = StatusQueued
				job.Error = "Recovered before insertion"
				_ = d.store.Save(job)
				d.enqueue(job.ID)
			}
		}
	}
}

func (d *Daemon) process(ctx context.Context, id string) {
	job, err := d.store.Get(id)
	if err != nil || job.Status == StatusDelivered || job.Status == StatusCancelled {
		return
	}
	if job.Transcript == "" {
		job.Status = StatusTranscribing
		_ = d.store.Save(job)
		d.setStatus("processing", "Transcribing")
		pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		text, err := ProviderFor(d.cfg).Transcribe(pctx, job.AudioPath, d.cfg)
		cancel()
		if err != nil {
			d.fail(job, err)
			return
		}
		job.Transcript = text
	}
	job.Status = StatusCleaning
	_ = d.store.Save(job)
	d.setStatus("processing", "Cleaning")
	text, cleanErr := Clean(ctx, job.Transcript, d.cfg)
	if cleanErr != nil {
		text = job.Transcript
		job.Error = "Cleanup skipped: " + cleanErr.Error()
	}
	job.FinalText = text
	job.Status = StatusDelivering
	_ = d.store.Save(job)
	d.setStatus("processing", "Inserting")
	job.DeliveryAttempted = true
	_ = d.store.Save(job)
	if err := d.deliver(job); err != nil {
		job.Status = StatusFailed
		job.ClipboardBackup = true
		job.Error = "Copied to clipboard: " + err.Error()
		_ = d.store.Save(job)
		notify("Dictation ready", "Text was copied to the clipboard. Click a field and paste it.")
		d.setStatus("idle", "Ready")
		return
	}
	job.Status = StatusDelivered
	job.DeliveredAt = time.Now().UTC()
	job.Error = ""
	_ = d.store.Save(job)
	d.setStatus("idle", "Ready")
}
func (d *Daemon) fail(job *Job, err error) {
	job.Attempts++
	if IsRetryable(err) && job.Attempts < d.cfg.Retry.MaxAttempts {
		delay := time.Duration(d.cfg.Retry.InitialSecs) * time.Second * time.Duration(1<<(job.Attempts-1))
		max := time.Duration(d.cfg.Retry.MaxSecs) * time.Second
		if delay > max {
			delay = max
		}
		job.Status = StatusRetryWait
		job.NextAttemptAt = time.Now().Add(delay)
		job.Error = err.Error()
		_ = d.store.Save(job)
		d.setStatus("queued", "Saved safely; retrying soon")
		return
	}
	job.Status = StatusFailed
	job.Error = err.Error()
	_ = d.store.Save(job)
	d.setStatus("error", "Dictation saved for retry")
	notify("Dictation saved for retry", "The recording is safe. Use dictationd retry-last when the service is available.")
	d.clearErrorSoon()
}

func (d *Daemon) clearErrorSoon() {
	go func() {
		time.Sleep(3 * time.Second)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.recording == nil && d.phase.Phase == "error" {
			d.setStatusLocked("idle", "Ready")
		}
	}()
}
func (d *Daemon) deliver(job *Job) error {
	if d.cfg.SafeInsertion && job.Target.Address != "" {
		current := activeWindow()
		if current.Address != job.Target.Address {
			_ = copyClipboard(job.FinalText)
			return errors.New("focused window changed")
		}
	}
	if err := exec.Command("wtype", job.FinalText).Run(); err != nil {
		_ = copyClipboard(job.FinalText)
		return err
	}
	return nil
}
func activeWindow() WindowTarget {
	b, err := exec.Command("hyprctl", "activewindow", "-j").Output()
	if err != nil {
		return WindowTarget{}
	}
	var out struct {
		Address string `json:"address"`
		Class   string `json:"class"`
		Title   string `json:"title"`
	}
	if json.Unmarshal(b, &out) != nil {
		return WindowTarget{}
	}
	return WindowTarget{Address: out.Address, Class: out.Class, Title: out.Title}
}
func copyClipboard(text string) error {
	cmd := exec.Command("wl-copy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
func notify(title, body string) {
	_ = exec.Command("notify-send", "--app-name=dictationd", title, body).Run()
}

func (d *Daemon) Status() Status { d.mu.Lock(); defer d.mu.Unlock(); return d.phase }
func (d *Daemon) setStatus(phase, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setStatusLocked(phase, msg)
}
func (d *Daemon) setStatusLocked(phase, msg string) {
	d.phase = Status{Phase: phase, Message: msg, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if d.recording != nil {
		d.phase.ActiveJobID = d.recording.jobID
	}
	b, _ := json.Marshal(d.phase)
	tmp := d.cfg.StatusPath() + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		_ = os.Rename(tmp, d.cfg.StatusPath())
	}
}

type wavWriter struct {
	file       *os.File
	bytes      uint32
	sampleRate int
	mu         sync.Mutex
}

func newWavWriter(path string, rate int) (*wavWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	w := &wavWriter{file: f, sampleRate: rate}
	if _, err := f.Write(make([]byte, 44)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, nil
}
func (w *wavWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.file.Write(b)
	w.bytes += uint32(n)
	return n, err
}
func (w *wavWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	h := wavHeader(w.bytes, w.sampleRate)
	if _, err := w.file.WriteAt(h, 0); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}
func wavHeader(bytes uint32, sampleRate int) []byte {
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], 36+bytes)
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1)
	binary.LittleEndian.PutUint16(h[22:24], 1)
	binary.LittleEndian.PutUint32(h[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(h[32:34], 2)
	binary.LittleEndian.PutUint16(h[34:36], 16)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], bytes)
	return h
}
func repairWAV(path string, sampleRate int) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < 44 {
		return errors.New("WAV is too short to repair")
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteAt(wavHeader(uint32(info.Size()-44), sampleRate), 0)
	return err
}
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func encodeBase64(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := make([]byte, ((len(b)+2)/3)*4)
	for i, j := 0, 0; i < len(b); i, j = i+3, j+4 {
		v := uint(b[i]) << 16
		if i+1 < len(b) {
			v |= uint(b[i+1]) << 8
		}
		if i+2 < len(b) {
			v |= uint(b[i+2])
		}
		out[j] = alphabet[(v>>18)&63]
		out[j+1] = alphabet[(v>>12)&63]
		if i+1 < len(b) {
			out[j+2] = alphabet[(v>>6)&63]
		} else {
			out[j+2] = '='
		}
		if i+2 < len(b) {
			out[j+3] = alphabet[v&63]
		} else {
			out[j+3] = '='
		}
	}
	return string(out)
}

// Keep syscall imported on older Go releases where interrupted pw-record exits
// with a signal; the reference prevents build differences across Arch versions.
var _ = syscall.SIGINT
