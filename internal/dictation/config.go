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
	DataDir          string        `json:"data_dir"`
	StateDir         string        `json:"state_dir"`
	RuntimeDir       string        `json:"runtime_dir"`
	MicTarget        string        `json:"mic_target"`
	SampleRate       int           `json:"sample_rate"`
	SafeInsertion    bool          `json:"safe_insertion"`
	FailedRetention  int           `json:"failed_audio_retention_days"`
	HistoryRetention int           `json:"history_retention_days"`
	Retry            RetryConfig   `json:"retry"`
	ASR              ASRConfig     `json:"asr"`
	Cleanup          CleanupConfig `json:"cleanup"`
	UI               UIConfig      `json:"ui"`
	Sound            SoundConfig   `json:"sound"`
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
		ASR:     ASRConfig{Provider: "elevenlabs_batch", APIKeyEnv: "ELEVENLABS_API_KEY", Language: "eng", Secondary: []string{"hin"}, NoVerbatim: true, Model: "scribe_v2", Endpoint: "https://api.elevenlabs.io/v1/speech-to-text"},
		Cleanup: CleanupConfig{Enabled: false, APIKeyEnv: "LLM_API_KEY", Endpoint: "", Model: ""},
		UI:      UIConfig{Enabled: true},
		Sound:   SoundConfig{Enabled: true},
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

func (c Config) SocketPath() string { return filepath.Join(c.RuntimeDir, "control.sock") }
func (c Config) StatusPath() string { return filepath.Join(c.RuntimeDir, "status.json") }
func (c Config) JobsDir() string    { return filepath.Join(c.DataDir, "jobs") }
func (c Config) VocabularyPath() string {
	return filepath.Join(defaultBase("XDG_CONFIG_HOME", ".config"), "dictationd", "vocabulary.json")
}
func (c Config) LibraryUIPath() string          { return filepath.Join(c.DataDir, "ui", "JFlow.qml") }
func (c Config) CleanupInterval() time.Duration { return 30 * time.Minute }
