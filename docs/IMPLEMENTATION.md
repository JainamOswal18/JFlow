# JFlow implementation handoff

## Goal

JFlow is a Wispr Flow-style dictation workflow for this Arch/Hyprland laptop:
hold `Super+R`, speak, release, then receive only the polished final text in
the previously focused application. It intentionally has no live transcript.

The essential reliability rule is simple: audio is persisted locally before a
cloud provider is trusted. A temporary cloud failure must not force a repeat
dictation.

## What was implemented

- A single Go binary, `dictationd`, with a small Unix-socket CLI.
- PipeWire capture through `pw-record` at 16 kHz mono PCM.
- Stable ElevenLabs Scribe v2 file transcription after release. The saved WAV
  means provider/network trouble never holds the microphone open.
- An experimental real-time Scribe adapter, retained for later benchmarking but
  not selected as the default hotkey path.
- Optional provider adapters for Sarvam REST and local `whisper-cli`.
- Optional OpenAI-compatible LLM cleanup with a deliberately conservative
  prompt. Cleanup failure keeps and delivers the raw ASR result.
- A durable, per-job JSON store with atomic writes. No database service,
  container, Python runtime, or audio is required in the source repository.
- One automatic retry after three seconds for retryable network/provider
  failures, with a restart-safe recovery scan as backup.
- A tiny standalone Quickshell status pill at the bottom center: listening,
  processing, retrying, or error. It has no access to raw audio, transcripts,
  or credentials.
- A separate Quickshell **JFlow Library** desktop window, opened on demand. It
  searches history and manages local vocabulary without making the recording
  overlay larger or permanently consuming desktop resources.
- Atomic local vocabulary corrections applied after ASR and before insertion.
  This avoids ElevenLabs' paid keyterm feature for ordinary names and terms.
- Hyprland hold/release binding for `Super+R` and a recovery binding on
  `Super+Shift+V`.
- User-level systemd units for the daemon and indicator.

## Why these choices

### Go daemon

Go gives one small native binary with straightforward process management,
concurrency, WebSocket streaming, and service deployment. It avoids a Python
virtual environment and keeps the always-running part of the setup easy to
restart and inspect.

### Saved WAV jobs instead of an in-memory queue

Every dictation gets a directory under the data location. Its `job.json` is
written atomically and the audio file is written as capture progresses. This
means Wi-Fi drops, API rate limits, and service restarts preserve a retriable
recording.

### Release-only text

The production path waits until release and sends the complete utterance. This
gives the model full context for filler removal and changed thoughts, without a
distracting live preview or a network operation in the hotkey start path.

### Safe text insertion

JFlow records the active Hyprland window at dictation start. If focus has
changed by the time text is ready, it copies text to the Wayland clipboard
instead of typing into the current window. It also persists a
`delivery_attempted` flag before `wtype` runs. After a crash in that narrow
window, the job requires explicit recovery rather than risking duplicated text.

## Installed locations on this laptop

| Purpose | Location |
| --- | --- |
| Source repository | `~/Code/dictationd` |
| Binary | `~/.local/bin/dictationd` |
| Settings | `~/.config/dictationd/config.json` |
| API keys (mode `0600`) | `~/.config/dictationd/credentials.env` |
| Vocabulary corrections | `~/.config/dictationd/vocabulary.json` |
| Queue and local history | `~/.local/share/dictationd/jobs/` |
| Runtime socket/status | `$XDG_RUNTIME_DIR/dictationd/` |
| User units | `~/.config/systemd/user/dictationd*.service` |
| Quickshell indicator | `~/.local/share/dictationd/ui/VoiceFlow.qml` |
| JFlow Library window | `~/.local/share/dictationd/ui/JFlow.qml` |
| Application launcher entry | `~/.local/share/applications/JFlow.desktop` |
| Hyprland bindings | `~/.config/hypr/custom/keybinds.lua` |

The Git repository deliberately contains no credentials, recordings, built
binary, or user-specific config.

## Current machine integration

- The `dictationd.service` and `dictationd-ui.service` user units are enabled
  and were confirmed active.
- Hyprland was reloaded after adding the bindings.
- The bindings use the absolute binary path because this Hyprland session's
  `PATH` does not include `~/.local/bin`.
- The configured PipeWire target is `easyeffects_source`. EasyEffects on this
  machine already has RNNoise and VAD enabled on its input chain.
- The local queue defaults to: one-hour successful-audio retention, 14-day
  failed-audio retention, and 30-day metadata/transcript retention.

## Phase 2: local history and vocabulary

The Library is a standard Quickshell `FloatingWindow`, so Hyprland treats it
like a normal desktop application. It starts only when `dictationd library` is
run (or the **JFlow** launcher entry is opened), uses `qs --no-duplicate` to
make repeat opens harmless, and can be closed without changing daemon state.

History stays in the existing durable job directories. The daemon exposes
search, exact-item copy, retry, and delete operations over its existing
user-only socket; the UI does not read audio files, credentials, or the socket
directly. Active jobs cannot be deleted.

Vocabulary is a small JSON array in `~/.config/dictationd/vocabulary.json`.
Writes use a same-directory temporary file followed by rename, with mode 0600.
Each `heard as → write as` rule is applied as a case-insensitive whole word or
phrase after transcription (and optional cleanup), before the final text is
saved, copied, and inserted. Longer phrases are applied first.

## First-use steps

1. Put an ElevenLabs key in `~/.config/dictationd/credentials.env`:

   ```ini
   ELEVENLABS_API_KEY=your_key
   ```

2. Restart the daemon so it reads the credential:

   ```bash
   systemctl --user restart dictationd.service
   ```

3. Focus a disposable text field, hold `Super+R`, say a sentence, and release.

4. Inspect results if necessary:

   ```bash
   dictationd status
   dictationd history
   journalctl --user -u dictationd.service -f
   ```

Do not place the credential file in this repository or OneDrive.

## Provider and cleanup configuration

`asr.provider` in `config.json` selects the recognition path:

- `elevenlabs_batch` (default): sends the saved WAV to Scribe v2 after release.
- `elevenlabs_realtime` (experimental): streams during capture; keep it off
  until it has been benchmarked without hotkey stalls on this desktop.
- `sarvam`: sends the saved WAV to Sarvam's REST endpoint. Use this after a
  personal accuracy comparison for Indian-accent English and occasional
  Hinglish.
- `whisper_cli`: runs local `whisper-cli`; set `asr.whisper_model` to the local
  model path first.

The cleanup section intentionally starts disabled. To enable it, provide an
OpenAI-compatible chat-completions endpoint, a model name, and `LLM_API_KEY`.
The code removes fillers and abandoned phrases only when unambiguous; it must
not summarize, translate, or invent text.

## Recovery behaviour

- Retryable provider/network errors receive one automatic retry after three
  seconds; the recording remains available for a manual retry afterward.
- Failed jobs retain the source audio for manual retry with
  `dictationd retry-last` or `Super+Shift+V`.
- A failed job can be removed from the active retry path with
  `dictationd dismiss-last`; an error indicator clears automatically after a
  brief flash.
- If cloud transcription works but insertion is unsafe or fails, the final text
  goes to the clipboard and the job is retained.
- On daemon restart, interrupted recordings are repaired and queued when audio
  exists; interrupted ASR/cleanup jobs are queued again.
- A crash during text insertion does **not** automatically retry insertion,
  because that could duplicate text. The final text remains available for
  deliberate recovery.

## Validation performed

- `go test ./...` passed.
- `go vet ./...` passed.
- The Go binary built successfully.
- Hyprland reload succeeded.
- Both systemd user units were enabled, started, and reported active.
- `dictationd status` returned `Ready`.
- The Quickshell indicator loaded successfully under the user service.

An end-to-end transcription was intentionally not sent: no API key was placed
in the credential file during implementation.

## Next steps

1. Deploy this branch, then confirm a real dictation appears in the Library and
   test Copy, Retry on a deliberately failed item, Delete, and one vocabulary
   correction.
2. Benchmark Scribe against Sarvam using 15–20 of your own English-first,
   Indian-accent sentences with names, technical terms, occasional Hinglish,
   corrections, and noisy-room samples. Choose one primary provider rather
   than chaining ASR systems.
3. Add frequent names, project terms, and commands through the local Library
   vocabulary rather than paid cloud keyterms.
4. Decide whether cleanup is needed after testing Scribe's `no_verbatim` mode.
   If enabled, test it carefully with technical text and code-related speech.
5. Phase 3 can add optional per-app profiles and hands-free mode.
