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
	cfg              Config
	store            *Store
	vocabulary       *VocabularyStore
	lifecycleMu      sync.Mutex
	mu               sync.Mutex
	recording        *recording
	processingID     string
	processingCancel context.CancelFunc
	phase            Status
	work             chan string
	langfuse         *LangfuseSink
}

type recording struct {
	jobID        string
	cmd          *exec.Cmd
	done         chan error
	realtime     *RealtimeScribe
	wav          *wavWriter
	handsFree    bool
	sampleRate   int
	startedAt    time.Time
	mu           sync.Mutex
	lastVoiceAt  time.Time
	voiceSeconds float64
	autoStopping bool
	stopping     bool
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
	d := &Daemon{cfg: cfg, store: store, vocabulary: NewVocabularyStore(cfg.VocabularyPath()), phase: Status{Phase: "idle"}, work: make(chan string, 32), langfuse: NewLangfuseSink(cfg)}
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
	if d.cfg.Formatter.Mode == "auto" {
		go func() {
			warmCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			WarmOllama(warmCtx, d.cfg.Formatter)
		}()
	}
	go d.worker(ctx)
	go d.recoveryLoop(ctx)
	go d.cleanupLoop(ctx)
	if d.langfuse != nil {
		go d.langfuse.Run(ctx)
	}
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
	Action      string `json:"action"`
	JobID       string `json:"job_id,omitempty"`
	Query       string `json:"query,omitempty"`
	Heard       string `json:"heard,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	Text        string `json:"text,omitempty"`
}
type Response struct {
	OK         bool                    `json:"ok"`
	Error      string                  `json:"error,omitempty"`
	Status     Status                  `json:"status"`
	Jobs       []*Job                  `json:"jobs,omitempty"`
	Vocabulary []VocabularyEntry       `json:"vocabulary,omitempty"`
	Dataset    []FormatterDatasetEntry `json:"dataset,omitempty"`
	Benchmarks []FormatterBenchmark    `json:"benchmarks,omitempty"`
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
		case "handsfree-start":
			err = d.StartHandsFree()
		case "handsfree-toggle":
			if d.isRecording() {
				err = d.Stop()
			} else {
				err = d.StartHandsFree()
			}
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
		case "cancel-if-recording":
			err = d.CancelIfRecording()
		case "retry-last":
			err = d.RetryLast()
		case "copy-last":
			err = d.CopyLast()
		case "copy":
			err = d.Copy(cmd.JobID)
		case "dismiss-last":
			err = d.DismissLast()
		case "retry":
			err = d.Retry(cmd.JobID)
		case "status":
		case "history":
			resp.Jobs, err = d.store.Search(cmd.Query)
		case "delete-history":
			err = d.DeleteHistory(cmd.JobID)
		case "correct-history":
			err = d.CorrectHistory(cmd.JobID, cmd.Text)
		case "learn-selection":
			err = d.LearnSelection()
		case "formatter-feedback":
			err = d.SetFormatterFeedback(cmd.JobID, cmd.Text)
		case "formatter-dataset":
			resp.Dataset, err = d.FormatterDataset()
		case "formatter-benchmark":
			resp.Benchmarks, err = d.BenchmarkFormatter(strings.Fields(cmd.Text))
		case "vocabulary":
			resp.Vocabulary, err = d.vocabulary.List()
		case "vocabulary-add":
			_, err = d.vocabulary.AddCanonical(cmd.Text)
			if err == nil {
				resp.Vocabulary, err = d.vocabulary.List()
			}
		case "vocabulary-delete":
			err = d.vocabulary.Delete(cmd.JobID)
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
	return d.start(false)
}

func (d *Daemon) StartHandsFree() error {
	if !d.cfg.HandsFree.Enabled {
		return errors.New("hands-free dictation is disabled")
	}
	return d.start(true)
}

func (d *Daemon) start(handsFree bool) error {
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
	target := activeWindow()
	job := &Job{ID: id, Status: StatusRecording, CreatedAt: now, AudioPath: d.store.AudioPath(id), Target: target, Formatting: FormattingInfo{ContextHint: InferContextHint(target)}}
	if err := d.store.Save(job); err != nil {
		return err
	}
	r, err := d.beginRecording(job, handsFree)
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		_ = d.store.Save(job)
		return err
	}
	d.recording = r
	if handsFree {
		d.setStatusLocked("recording", "Listening hands-free")
	} else {
		d.setStatusLocked("recording", "Listening")
	}
	d.playCue("message-new-instant")
	return nil
}

func (d *Daemon) beginRecording(job *Job, handsFree bool) (*recording, error) {
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
	r := &recording{jobID: job.ID, cmd: cmd, done: make(chan error, 1), handsFree: handsFree, sampleRate: d.cfg.SampleRate, startedAt: time.Now()}
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
				if r.shouldAutoStop(chunk, d.cfg.HandsFree) {
					go func() { _ = d.stopRecording(r, "Silence detected") }()
				}
			}
			if readErr != nil {
				if readErr != io.EOF && !(r.stopRequested() && isExpectedPipeClose(readErr)) {
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

func (r *recording) shouldAutoStop(pcm []byte, cfg HandsFreeConfig) bool {
	if !r.handsFree || !hasVoice(pcm, cfg.VoiceThreshold) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.handsFree || r.lastVoiceAt.IsZero() || r.autoStopping {
			return false
		}
		if time.Since(r.lastVoiceAt) < time.Duration(cfg.SilenceSecs*float64(time.Second)) {
			return false
		}
		r.autoStopping = true
		return true
	}
	r.mu.Lock()
	r.lastVoiceAt = time.Now()
	r.voiceSeconds += float64(len(pcm)) / float64(2*r.sampleRate)
	r.mu.Unlock()
	return false
}

func (r *recording) spokenSeconds() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.voiceSeconds
}

func (r *recording) markStopping() {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
}

func (r *recording) stopRequested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopping
}

// cmd.Wait closes StdoutPipe after pw-record exits. During an intentional
// stop, the reader can therefore see os.ErrClosed instead of io.EOF even
// though all captured audio has already been written to the WAV file.
func isExpectedPipeClose(err error) bool {
	return errors.Is(err, os.ErrClosed) || strings.Contains(strings.ToLower(err.Error()), "file already closed")
}

func hasVoice(pcm []byte, threshold int) bool {
	if len(pcm) < 2 {
		return false
	}
	var total int64
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		if sample < 0 {
			total -= int64(sample)
		} else {
			total += int64(sample)
		}
	}
	return total/int64(len(pcm)/2) >= int64(threshold)
}

func (d *Daemon) Stop() error {
	d.mu.Lock()
	r := d.recording
	d.mu.Unlock()
	if r == nil {
		return errors.New("not recording")
	}
	return d.stopRecording(r, "Finalizing audio")
}

func (d *Daemon) stopRecording(r *recording, message string) error {
	d.mu.Lock()
	if d.recording != r {
		d.mu.Unlock()
		return errors.New("not recording")
	}
	d.recording = nil
	d.setStatusLocked("processing", message)
	d.mu.Unlock()
	r.markStopping()
	if err := r.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		// The recording has already been detached from the active state. Make a
		// best effort to terminate its child process rather than leave the UI in
		// Listening/Finalizing forever.
		_ = r.cmd.Process.Kill()
	}
	// pw-record exits with status 1 on SIGINT on this PipeWire build. We sent
	// that signal deliberately to finalize the recording, so the WAV/pipe result
	// (not the process exit code) determines whether capture succeeded.
	waitDone := make(chan struct{}, 1)
	go func() {
		_ = r.cmd.Wait()
		waitDone <- struct{}{}
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		_ = r.cmd.Process.Kill()
		select {
		case <-waitDone:
		case <-time.After(1 * time.Second):
		}
	}
	var pipeErr error
	select {
	case pipeErr = <-r.done:
	case <-time.After(2 * time.Second):
		pipeErr = errors.New("microphone capture did not close within 2 seconds")
	}
	if err := r.wav.Close(); err != nil {
		pipeErr = err
	}
	d.playCue("complete")
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
	} else {
		job.RecordingSeconds = float64(info.Size()-44) / float64(2*d.cfg.SampleRate)
	}
	if r.handsFree && r.spokenSeconds() < d.cfg.HandsFree.MinSpeechSecs {
		job.Status = StatusCancelled
		job.Error = "No speech captured (hands-free minimum not reached)"
		_ = d.store.Save(job)
		_ = os.Remove(job.AudioPath)
		d.setStatus("idle", "Ready")
		return nil
	}
	if pipeErr != nil {
		job.Status = StatusFailed
		job.Error = pipeErr.Error()
		_ = d.store.Save(job)
		d.showAction("error", "Microphone capture failed", job.ID, true)
		notify("Microphone capture failed", "The recording is saved. Retry is available.")
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
	d.mu.Lock()
	r := d.recording
	if r == nil {
		d.mu.Unlock()
		return errors.New("not recording")
	}
	d.recording = nil
	d.mu.Unlock()
	_ = r.cmd.Process.Kill()
	waitDone := make(chan struct{}, 1)
	go func() { _ = r.cmd.Wait(); waitDone <- struct{}{} }()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
	}
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
	}
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
	d.playCue("dialog-warning")
	d.setStatus("idle", "Ready")
	return nil
}

func (d *Daemon) CancelIfRecording() error {
	if d.isRecording() {
		return d.Cancel()
	}
	d.mu.Lock()
	cancel := d.processingCancel
	d.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	d.setStatus("processing", "Cancelling")
	return nil
}
func (d *Daemon) Retry(id string) error {
	d.lifecycleMu.Lock()
	defer d.lifecycleMu.Unlock()
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

func (d *Daemon) DeleteHistory(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("job ID is required")
	}
	d.lifecycleMu.Lock()
	defer d.lifecycleMu.Unlock()
	d.mu.Lock()
	busy := (d.recording != nil && d.recording.jobID == id) || d.processingID == id
	d.mu.Unlock()
	if busy {
		return errors.New("cannot delete an active dictation")
	}
	return d.store.Delete(id)
}

// CorrectHistory saves a user-edited transcript and learns only close spelling
// or spacing aliases from the before/after text. The learning is local; only
// canonical vocabulary terms are ever sent to Scribe on future requests.
func (d *Daemon) CorrectHistory(id, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("corrected text is required")
	}
	d.lifecycleMu.Lock()
	defer d.lifecycleMu.Unlock()
	d.mu.Lock()
	busy := (d.recording != nil && d.recording.jobID == id) || d.processingID == id
	d.mu.Unlock()
	if busy {
		return errors.New("cannot correct an active dictation")
	}
	job, err := d.store.Get(id)
	if err != nil {
		return err
	}
	before := job.Transcript
	if strings.TrimSpace(before) == "" {
		before = job.FinalText
	}
	if _, err := d.vocabulary.LearnFromCorrection(before, text); err != nil {
		return err
	}
	job.FinalText = text
	if job.Formatting.Eligible {
		job.Formatting.Feedback = "corrected"
	}
	return d.store.Save(job)
}

// LearnSelection learns one close spelling/spacing correction from text the
// user explicitly selected in their current app. It never watches the
// clipboard or UI passively: this runs only through its explicit command or
// hotkey. The latest delivered transcript is the only comparison source.
func (d *Daemon) LearnSelection() error {
	selected, err := readExplicitSelection()
	if err != nil {
		return err
	}
	jobs, err := d.store.List()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Status != StatusDelivered || strings.TrimSpace(job.Transcript) == "" {
			continue
		}
		learned, err := d.vocabulary.LearnFromSelection(job.Transcript, selected)
		if err != nil {
			return err
		}
		if !learned {
			return errors.New("selected text is not a close spelling correction of the latest dictation")
		}
		d.setStatus("idle", "Learned selected vocabulary")
		return nil
	}
	return errors.New("no delivered dictation is available to learn from")
}

func readExplicitSelection() (string, error) {
	// Primary selection is Wayland's highlighted-text channel. Clipboard is
	// only tried when the user deliberately copied the correction first.
	for _, args := range [][]string{{"--primary", "--no-newline"}, {"--no-newline"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, "wl-paste", args...).Output()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			return "", errors.New("reading selected text timed out")
		}
		if text := strings.TrimSpace(string(out)); err == nil && text != "" {
			return text, nil
		}
	}
	return "", errors.New("select the corrected word or phrase first")
}

func (d *Daemon) SetFormatterFeedback(id, feedback string) error {
	feedback = strings.TrimSpace(strings.ToLower(feedback))
	if feedback != "helpful" && feedback != "needs_work" {
		return errors.New("feedback must be helpful or needs_work")
	}
	job, err := d.store.Get(id)
	if err != nil {
		return err
	}
	if !job.Formatting.Eligible {
		return errors.New("this dictation was not formatter-eligible")
	}
	job.Formatting.Feedback = feedback
	return d.store.Save(job)
}

// FormatterDataset returns only examples the user has explicitly endorsed or
// corrected. It is local output for comparing local Ollama models; it never
// writes back to jobs, starts during dictation, or queues Langfuse telemetry.
func (d *Daemon) FormatterDataset() ([]FormatterDatasetEntry, error) {
	jobs, err := d.store.List()
	if err != nil {
		return nil, err
	}
	entries := make([]FormatterDatasetEntry, 0)
	for _, job := range jobs {
		f := job.Formatting
		if !f.Eligible || (f.Feedback != "helpful" && f.Feedback != "corrected") {
			continue
		}
		input := strings.TrimSpace(f.InputText)
		if input == "" {
			input = strings.TrimSpace(job.Transcript)
		}
		expected := strings.TrimSpace(job.FinalText)
		if input == "" || expected == "" {
			continue
		}
		entries = append(entries, FormatterDatasetEntry{JobID: job.ID, Input: input, Expected: expected, ContextHint: f.ContextHint, Feedback: f.Feedback})
	}
	return entries, nil
}

// BenchmarkFormatter runs an explicit, local-only comparison against the
// reviewed dataset. It is refused while dictation is active so a benchmark can
// never delay recording, transcription, or delivery. Model outputs are merely
// reported for review: JFlow does not auto-select a model or rewrite history.
func (d *Daemon) BenchmarkFormatter(models []string) ([]FormatterBenchmark, error) {
	if len(models) == 0 {
		return nil, errors.New("provide one or more local Ollama model names")
	}
	if d.isRecording() {
		return nil, errors.New("cannot benchmark while dictating")
	}
	d.mu.Lock()
	processing := d.processingID != ""
	d.mu.Unlock()
	if processing {
		return nil, errors.New("cannot benchmark while JFlow is processing a dictation")
	}
	dataset, err := d.FormatterDataset()
	if err != nil {
		return nil, err
	}
	if len(dataset) == 0 {
		return nil, errors.New("no reviewed formatter examples yet; mark outputs Useful or save a History correction first")
	}
	benchmarks := make([]FormatterBenchmark, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		cfg := d.cfg.Formatter
		cfg.Mode = "auto"
		cfg.Model = model
		benchmark := FormatterBenchmark{Model: model, Cases: make([]FormatterBenchmarkCase, 0, len(dataset))}
		var totalLatency int64
		var completed int64
		for _, entry := range dataset {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSecs)*time.Second)
			result, formatErr := FormatWithOllama(ctx, entry.Input, entry.ContextHint, cfg)
			cancel()
			item := FormatterBenchmarkCase{JobID: entry.JobID, Output: result.Text, LatencyMS: result.Audit.LatencyMS}
			if formatErr != nil {
				item.Error = formatErr.Error()
			} else {
				totalLatency += item.LatencyMS
				completed++
			}
			benchmark.Cases = append(benchmark.Cases, item)
		}
		if completed > 0 {
			benchmark.AverageLatencyMS = totalLatency / completed
		}
		benchmarks = append(benchmarks, benchmark)
	}
	if len(benchmarks) == 0 {
		return nil, errors.New("provide one or more local Ollama model names")
	}
	return benchmarks, nil
}

func usageForASR(cfg Config, seconds float64) ASRUsage {
	provider := cfg.ASR.Provider
	return ASRUsage{Provider: provider, Model: cfg.ASR.Model, Cloud: provider != "whisper_cli", AudioSeconds: seconds}
}

func (d *Daemon) CopyLast() error {
	jobs, err := d.store.List()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if strings.TrimSpace(j.FinalText) != "" {
			return d.Copy(j.ID)
		}
	}
	return errors.New("no completed dictation to copy")
}

func (d *Daemon) Copy(id string) error {
	job, err := d.store.Get(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(job.FinalText) == "" {
		return errors.New("dictation has no text to copy")
	}
	if err := copyClipboard(job.FinalText); err != nil {
		d.showAction("error", "Copy failed", job.ID, false)
		return err
	}
	d.showAction("copied", "Copied", job.ID, false)
	return nil
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
			d.lifecycleMu.Lock()
			jobCtx, cancel := context.WithCancel(ctx)
			d.mu.Lock()
			d.processingID = id
			d.processingCancel = cancel
			d.mu.Unlock()
			d.lifecycleMu.Unlock()
			d.process(jobCtx, id)
			cancel()
			d.lifecycleMu.Lock()
			d.mu.Lock()
			if d.processingID == id {
				d.processingID = ""
				d.processingCancel = nil
			}
			d.mu.Unlock()
			d.lifecycleMu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
func (d *Daemon) recoveryLoop(ctx context.Context) {
	// This is only a restart-safety net. Normal retries are scheduled directly
	// when an attempt fails, so they do not wait for the next scan.
	t := time.NewTicker(5 * time.Second)
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
	if d.langfuse != nil {
		d.langfuse.Cleanup(time.Duration(d.cfg.HistoryRetention) * 24 * time.Hour)
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
		case StatusTranscribing, StatusCleaning, StatusFormatting:
			job.Status = StatusQueued
			job.Error = "Recovered after service restart"
			_ = d.store.Save(job)
			d.enqueue(job.ID)
		case StatusDelivering:
			if job.DeliveryAttempted {
				job.Status = StatusFailed
				job.ClipboardBackup = false
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
	if d.cancelledProcessing(ctx, job) {
		return
	}
	if job.Transcript == "" {
		job.Status = StatusTranscribing
		_ = d.store.Save(job)
		d.setStatus("processing", "Transcribing")
		// Short-form dictation should not sit in Transcribing for a minute.
		// A timed-out request is preserved locally and gets one quick retry.
		pctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		requestCfg := d.cfg
		// Canonical vocabulary terms bias Scribe toward the spelling the user
		// chose. Learned aliases stay local and are never sent to ElevenLabs.
		if keyterms, keytermErr := d.vocabulary.Keyterms(100); keytermErr == nil {
			requestCfg.ASR.Keyterms = mergeKeyterms(requestCfg.ASR.Keyterms, keyterms, 100)
		}
		text, err := ProviderFor(requestCfg).Transcribe(pctx, job.AudioPath, requestCfg)
		cancel()
		if d.cancelledProcessing(ctx, job) {
			return
		}
		if err != nil {
			d.fail(job, err)
			return
		}
		job.Transcript = text
		job.Usage = usageForASR(requestCfg, job.RecordingSeconds)
	}
	job.Status = StatusCleaning
	_ = d.store.Save(job)
	d.setStatus("processing", "Cleaning")
	text, cleanErr := Clean(ctx, job.Transcript, d.cfg)
	if d.cancelledProcessing(ctx, job) {
		return
	}
	if cleanErr != nil {
		text = job.Transcript
		job.Error = "Cleanup skipped: " + cleanErr.Error()
	}
	text = d.vocabulary.Apply(text)
	// Retries must not inherit an earlier formatting result. Preserve only the
	// local context hint captured when recording began, then record this pass.
	job.Formatting = freshFormattingInfo(job.Formatting, d.cfg.Formatter.Mode == "auto" && job.RecordingSeconds > d.cfg.Formatter.MinRecordingSecs)
	if job.Formatting.Eligible {
		job.Status = StatusFormatting
		_ = d.store.Save(job)
		d.setStatus("processing", "Formatting")
		formatCtx, cancel := context.WithTimeout(ctx, time.Duration(d.cfg.Formatter.TimeoutSecs)*time.Second)
		originalText := text
		beforeFormatting := originalText
		preprocessRules := []string(nil)
		if normalized, applied := normalizeSpokenOrdinals(beforeFormatting); applied {
			beforeFormatting = normalized
			preprocessRules = []string{"spoken_ordinals_to_numbered_list"}
		}
		result, formatErr := FormatWithOllama(formatCtx, beforeFormatting, job.Formatting.ContextHint, d.cfg.Formatter)
		cancel()
		job.Formatting = result.Audit
		job.Formatting.Eligible = true
		job.Formatting.PreprocessRules = preprocessRules
		if d.cancelledProcessing(ctx, job) {
			return
		}
		if formatErr != nil {
			job.Formatting.Skipped = formatErr.Error()
		} else {
			text = result.Text
			job.Formatting.Applied = true
			job.Formatting.Changed = text != originalText
		}
	}
	job.FinalText = text
	if job.Formatting.Eligible && d.langfuse != nil {
		// Queueing is local and atomic. Network synchronization happens on its
		// own worker, never in the dictation or insertion path.
		d.langfuse.QueueFormatter(job)
	}
	job.Status = StatusDelivering
	_ = d.store.Save(job)
	d.setStatus("processing", "Inserting")
	if d.cancelledProcessing(ctx, job) {
		return
	}
	// Do not alter the user's clipboard on a successful insertion. The final
	// text is always retained in local history; the clipboard is a recovery path
	// only when insertion is unsafe or fails.
	job.ClipboardBackup = false
	job.DeliveryAttempted = true
	_ = d.store.Save(job)
	if err := d.deliver(job); err != nil {
		job.Status = StatusFailed
		clipboardErr := copyClipboard(job.FinalText)
		if clipboardErr == nil {
			job.ClipboardBackup = true
			job.Error = "Copied to clipboard: " + err.Error()
			notify("Dictation ready", "Text was copied to the clipboard. Click a field and paste it.")
			d.showAction("copied", "Copied to clipboard", job.ID, false)
		} else {
			job.Error = fmt.Sprintf("Text could not be inserted or copied: %v (clipboard: %v)", err, clipboardErr)
			notify("Dictation delivery failed", "Text could not be inserted or copied. The transcript is retained in JFlow history.")
			d.showAction("error", "Delivery failed", job.ID, true)
		}
		_ = d.store.Save(job)
		return
	}
	job.Status = StatusDelivered
	job.DeliveredAt = time.Now().UTC()
	job.Error = ""
	_ = d.store.Save(job)
	message := "Inserted"
	if job.Formatting.Eligible && !job.Formatting.Applied {
		message = "Inserted — formatting skipped"
	}
	d.showAction("delivered", message, job.ID, false)
}

func freshFormattingInfo(previous FormattingInfo, eligible bool) FormattingInfo {
	return FormattingInfo{Eligible: eligible, ContextHint: previous.ContextHint}
}

func (d *Daemon) cancelledProcessing(ctx context.Context, job *Job) bool {
	if ctx.Err() == nil {
		return false
	}
	job.Status = StatusCancelled
	job.Error = "Cancelled during processing"
	_ = d.store.Save(job)
	_ = os.Remove(job.AudioPath)
	d.playCue("dialog-warning")
	d.setStatus("idle", "Ready")
	return true
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
		d.setStatus("retrying", fmt.Sprintf("Retrying %d/%d", job.Attempts, d.cfg.Retry.MaxAttempts))
		d.scheduleRetry(job.ID, delay)
		return
	}
	job.Status = StatusFailed
	job.Error = err.Error()
	_ = d.store.Save(job)
	d.playCue("dialog-error")
	d.showAction("error", "Dictation saved for retry", job.ID, true)
	notify("Dictation saved for retry", "The recording is safe. Use dictationd retry-last when the service is available.")
}

func (d *Daemon) scheduleRetry(id string, delay time.Duration) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		d.enqueue(id)
	}()
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
			return errors.New("focused window changed")
		}
	}
	if err := typeText(job.FinalText); err != nil {
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
		PID     int    `json:"pid"`
	}
	if json.Unmarshal(b, &out) != nil {
		return WindowTarget{}
	}
	return WindowTarget{Address: out.Address, Class: out.Class, Title: out.Title, PID: out.PID}
}
func copyClipboard(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wl-copy", "--type", "text/plain;charset=utf-8")
	cmd.Stdin = strings.NewReader(text)
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("wl-copy timed out after 2 seconds: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("wl-copy: %w", err)
	}
	// wl-copy's parent can exit before the background selection owner is ready.
	// Read the selection back before reporting success, so UI feedback is never a
	// best-effort claim.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer verifyCancel()
	got, err := exec.CommandContext(verifyCtx, "wl-paste", "--no-newline").Output()
	if verifyCtx.Err() != nil {
		return fmt.Errorf("clipboard verification timed out: %w", verifyCtx.Err())
	}
	if err != nil {
		return fmt.Errorf("clipboard verification: %w", err)
	}
	if string(got) != text {
		return errors.New("clipboard verification did not match copied text")
	}
	return nil
}

func typeText(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, args := range wtypeCommands(text) {
		if err := exec.CommandContext(ctx, "wtype", args...).Run(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("wtype timed out after 2 seconds: %w", ctx.Err())
			}
			return fmt.Errorf("wtype %q: %w", args, err)
		}
	}
	return nil
}

// wtype treats newlines in text as Enter keypresses. In chat applications
// that submits the message, so insert each line separately and use
// Shift+Enter for literal line breaks instead.
func wtypeCommands(text string) [][]string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	commands := make([][]string, 0, len(lines)*2)
	for i, line := range lines {
		if line != "" {
			// -- ensures a dictated line beginning with '-' is text, not an option.
			commands = append(commands, []string{"--", line})
		}
		if i < len(lines)-1 {
			commands = append(commands, []string{"-M", "shift", "-k", "Return"})
		}
	}
	return commands
}
func notify(title, body string) {
	_ = exec.Command("notify-send", "--app-name=dictationd", title, body).Run()
}

func (d *Daemon) playCue(event string) {
	if !d.cfg.Sound.Enabled {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "canberra-gtk-play", "-i", event).Run()
	}()
}

func (d *Daemon) Status() Status { d.mu.Lock(); defer d.mu.Unlock(); return d.phase }
func (d *Daemon) setStatus(phase, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setStatusLocked(phase, msg)
}

func (d *Daemon) setStatusLocked(phase, msg string) {
	d.setActionStatusLocked(phase, msg, "", false, false)
	if d.recording != nil {
		d.phase.ActiveJobID = d.recording.jobID
	}
}

func (d *Daemon) setActionStatusLocked(phase, msg, jobID string, canUndo, canRetry bool) {
	d.phase = Status{Phase: phase, Message: msg, ActionJobID: jobID, CanCopy: jobID != "", CanRetry: canRetry, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if d.recording != nil {
		d.phase.ActiveJobID = d.recording.jobID
	} else if d.processingID != "" {
		d.phase.ActiveJobID = d.processingID
	}
	b, _ := json.Marshal(d.phase)
	tmp := d.cfg.StatusPath() + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		_ = os.Rename(tmp, d.cfg.StatusPath())
	}
}

func (d *Daemon) showAction(phase, message, jobID string, canRetry bool) {
	d.mu.Lock()
	d.setActionStatusLocked(phase, message, jobID, false, canRetry)
	d.mu.Unlock()
	go func() {
		time.Sleep(8 * time.Second)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.recording == nil && d.phase.ActionJobID == jobID && d.phase.Phase == phase {
			d.setStatusLocked("idle", "Ready")
		}
	}()
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

func mergeKeyterms(existing, vocabulary []string, limit int) []string {
	seen := make(map[string]bool, len(existing)+len(vocabulary))
	merged := make([]string, 0, len(existing)+len(vocabulary))
	for _, term := range append(existing, vocabulary...) {
		term = strings.TrimSpace(term)
		key := strings.ToLower(term)
		if term == "" || seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, term)
		if limit > 0 && len(merged) >= limit {
			break
		}
	}
	return merged
}

// Keep syscall imported on older Go releases where interrupted pw-record exits
// with a signal; the reference prevents build differences across Arch versions.
var _ = syscall.SIGINT
