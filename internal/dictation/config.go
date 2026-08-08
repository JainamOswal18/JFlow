package dictation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	DataDir          string          `json:"data_dir"`
	StateDir         string          `json:"state_dir"`
	RuntimeDir       string          `json:"runtime_dir"`
	VocabularyFile   string          `json:"vocabulary_path,omitempty"`
	MicTarget        string          `json:"mic_target"`
	SampleRate       int             `json:"sample_rate"`
	SafeInsertion    bool            `json:"safe_insertion"`
	FailedRetention  int             `json:"failed_audio_retention_days"`
	HistoryRetention int             `json:"history_retention_days"`
	Retry            RetryConfig     `json:"retry"`
	ASR              ASRConfig       `json:"asr"`
	Cleanup          CleanupConfig   `json:"cleanup"`
	Formatter        FormatterConfig `json:"formatter"`
	Langfuse         LangfuseConfig  `json:"langfuse"`
	HandsFree        HandsFreeConfig `json:"hands_free"`
	UI               UIConfig        `json:"ui"`
	Sound            SoundConfig     `json:"sound"`
}

type RetryConfig struct {
	MaxAttempts int `json:"max_attempts"`
	InitialSecs int `json:"initial_delay_seconds"`
	MaxSecs     int `json:"max_delay_seconds"`
}

type ASRConfig struct {
	Provider      string   `json:"provider"` // elevenlabs_realtime, elevenlabs_batch, sarvam, whisper_cli
	APIKeyEnv     string   `json:"api_key_env"`
	Language      string   `json:"language"`
	Secondary     []string `json:"secondary_languages"`
	Keyterms      []string `json:"keyterms"`
	NoVerbatim    bool     `json:"no_verbatim"`
	Endpoint      string   `json:"endpoint"`
	Model         string   `json:"model"`
	WhisperBinary string   `json:"whisper_binary"`
	WhisperModel  string   `json:"whisper_model"`
}

type CleanupConfig struct {
	Enabled   bool   `json:"enabled"`
	APIKeyEnv string `json:"api_key_env"`
	Endpoint  string `json:"endpoint"`
	Model     string `json:"model"`
}

// FormatterConfig controls the optional, entirely local post-ASR formatting
// pass. Mode is "auto" or "off". Keeping this distinct from Cleanup means
// ElevenLabs can stay responsible for transcription cleanup while a local
// model only structures longer dictations.
type FormatterConfig struct {
	Mode             string  `json:"mode"`
	Endpoint         string  `json:"endpoint"`
	Model            string  `json:"model"`
	MinRecordingSecs float64 `json:"min_recording_seconds"`
	TimeoutSecs      int     `json:"timeout_seconds"`
	KeepAlive        string  `json:"keep_alive"`
	ContextTokens    int     `json:"context_tokens"`
	MaxOutputTokens  int     `json:"max_output_tokens"`
}

// LangfuseConfig controls local-first formatter telemetry. Credentials are
// intentionally read only from credentials.env, never from this JSON file.
type LangfuseConfig struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	PublicKeyEnv     string `json:"public_key_env"`
	SecretKeyEnv     string `json:"secret_key_env"`
	SyncIntervalSecs int    `json:"sync_interval_seconds"`
	TimeoutSecs      int    `json:"timeout_seconds"`
}

type HandsFreeConfig struct {
	Enabled        bool    `json:"enabled"`
	SilenceSecs    float64 `json:"silence_seconds"`
	MinSpeechSecs  float64 `json:"min_speech_seconds"`
	VoiceThreshold int     `json:"voice_threshold"`
}

type UIConfig struct {
	Enabled bool `json:"enabled"`
}

type SoundConfig struct {
	Enabled bool `json:"enabled"`
}

func defaultBase(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), fallback)
}

func DefaultConfig() Config {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join("/tmp", fmt.Sprintf("dictationd-%d", os.Getuid()))
	}
	return Config{
		DataDir:    filepath.Join(defaultBase("XDG_DATA_HOME", ".local/share"), "dictationd"),
		StateDir:   filepath.Join(defaultBase("XDG_STATE_HOME", ".local/state"), "dictationd"),
		RuntimeDir: filepath.Join(runtime, "dictationd"),
		// This source already exists on this machine and is the EasyEffects input
		// chain (RNNoise/VAD). Set it to "" to use PipeWire's default source.
		MicTarget: "easyeffects_source", SampleRate: 16000, SafeInsertion: true, FailedRetention: 14, HistoryRetention: 30,
		// One automatic retry keeps brief network failures invisible without
		// allowing a stalled cloud provider to hold up dictation indefinitely.
		Retry: RetryConfig{MaxAttempts: 2, InitialSecs: 3, MaxSecs: 120},
		// The release-only flow keeps the hotkey path local and instant: no network
		// call can leave microphone capture stuck while the key is held.
		ASR:       ASRConfig{Provider: "elevenlabs_batch", APIKeyEnv: "ELEVENLABS_API_KEY", Language: "eng", Secondary: []string{"hin"}, NoVerbatim: true, Model: "scribe_v2", Endpoint: "https://api.elevenlabs.io/v1/speech-to-text"},
		Cleanup:   CleanupConfig{Enabled: false, APIKeyEnv: "LLM_API_KEY", Endpoint: "", Model: ""},
		Formatter: FormatterConfig{Mode: "auto", Endpoint: "http://127.0.0.1:11434", Model: "qwen3:1.7b", MinRecordingSecs: 15, TimeoutSecs: 15, KeepAlive: "15m", ContextTokens: 2048, MaxOutputTokens: 512},
		Langfuse:  LangfuseConfig{Enabled: true, BaseURL: "https://cloud.langfuse.com", PublicKeyEnv: "LANGFUSE_PUBLIC_KEY", SecretKeyEnv: "LANGFUSE_SECRET_KEY", SyncIntervalSecs: 30, TimeoutSecs: 8},
		HandsFree: HandsFreeConfig{Enabled: true, SilenceSecs: 1.4, MinSpeechSecs: 0.4, VoiceThreshold: 650},
		UI:        UIConfig{Enabled: true},
		Sound:     SoundConfig{Enabled: true},
	}
}

func ConfigPath() string {
	return filepath.Join(defaultBase("XDG_CONFIG_HOME", ".config"), "dictationd", "config.json")
}
func CredentialsPath() string {
	return filepath.Join(defaultBase("XDG_CONFIG_HOME", ".config"), "dictationd", "credentials.env")
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.DataDir == "" || cfg.RuntimeDir == "" {
		return cfg, fmt.Errorf("data_dir and runtime_dir must not be empty")
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Retry.MaxAttempts <= 0 {
		cfg.Retry.MaxAttempts = 2
	}
	if cfg.Retry.InitialSecs <= 0 {
		cfg.Retry.InitialSecs = 3
	}
	if cfg.Retry.MaxSecs <= 0 {
		cfg.Retry.MaxSecs = 120
	}
	if cfg.Formatter.Mode == "" {
		cfg.Formatter.Mode = "auto"
	}
	if cfg.Formatter.Endpoint == "" {
		cfg.Formatter.Endpoint = "http://127.0.0.1:11434"
	}
	if cfg.Formatter.Model == "" {
		cfg.Formatter.Model = "qwen3:1.7b"
	}
	if cfg.Formatter.MinRecordingSecs <= 0 {
		cfg.Formatter.MinRecordingSecs = 15
	}
	if cfg.Formatter.TimeoutSecs <= 0 {
		cfg.Formatter.TimeoutSecs = 15
	}
	if cfg.Formatter.KeepAlive == "" {
		cfg.Formatter.KeepAlive = "15m"
	}
	if cfg.Formatter.ContextTokens <= 0 {
		cfg.Formatter.ContextTokens = 2048
	}
	if cfg.Formatter.MaxOutputTokens <= 0 {
		cfg.Formatter.MaxOutputTokens = 512
	}
	if cfg.Langfuse.BaseURL == "" {
		cfg.Langfuse.BaseURL = "https://cloud.langfuse.com"
	}
	if cfg.Langfuse.PublicKeyEnv == "" {
		cfg.Langfuse.PublicKeyEnv = "LANGFUSE_PUBLIC_KEY"
	}
	if cfg.Langfuse.SecretKeyEnv == "" {
		cfg.Langfuse.SecretKeyEnv = "LANGFUSE_SECRET_KEY"
	}
	if cfg.Langfuse.SyncIntervalSecs <= 0 {
		cfg.Langfuse.SyncIntervalSecs = 30
	}
	if cfg.Langfuse.TimeoutSecs <= 0 {
		cfg.Langfuse.TimeoutSecs = 8
	}
	if cfg.HandsFree.SilenceSecs <= 0 {
		cfg.HandsFree.SilenceSecs = 1.4
	}
	if cfg.HandsFree.MinSpeechSecs <= 0 {
		cfg.HandsFree.MinSpeechSecs = 0.4
	}
	if cfg.HandsFree.VoiceThreshold <= 0 {
		cfg.HandsFree.VoiceThreshold = 650
	}
	return cfg, nil
}

func WriteDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	b, err := json.MarshalIndent(DefaultConfig(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func LoadCredentials(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid credentials line: %q", raw)
		}
		if err := os.Setenv(strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), "\"'")); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) SocketPath() string       { return filepath.Join(c.RuntimeDir, "control.sock") }
func (c Config) StatusPath() string       { return filepath.Join(c.RuntimeDir, "status.json") }
func (c Config) JobsDir() string          { return filepath.Join(c.DataDir, "jobs") }
func (c Config) LangfuseQueueDir() string { return filepath.Join(c.StateDir, "langfuse", "pending") }
func (c Config) VocabularyPath() string {
	if c.VocabularyFile != "" {
		return c.VocabularyFile
	}
	return filepath.Join(defaultBase("XDG_CONFIG_HOME", ".config"), "dictationd", "vocabulary.json")
}
func (c Config) LibraryUIPath() string          { return filepath.Join(c.DataDir, "ui", "JFlow.qml") }
func (c Config) CleanupInterval() time.Duration { return 30 * time.Minute }
