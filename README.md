# JFlow (`dictationd`)

> A local-first, Wispr Flow-inspired dictation app for Arch Linux, Hyprland,
> and Wayland.

## Description

JFlow is a push-to-talk writing workflow: hold a key, speak, release, and have
cleaned text inserted into the app you were using. It captures through
PipeWire, retains the recording locally while it works, transcribes with
ElevenLabs Scribe v2, and can format longer dictations with a local Qwen model.
It deliberately has no distracting live transcript and never loses a recording
just because a provider or network request fails.

## Why I built JFlow

I recently switched back to Linux after leaving my last company and had grown
used to writing with Wispr Flow. Back on Arch Linux, there was no Wispr Flow or
alternative that felt right for my setup, so I built my own: **JFlow**. It came
together in two to three days using free/local models and a transcription
provider's free tier, with a focus on the things I actually missed: fast
dictation, reliable retries, good formatting, and a minimal UI.

## What it does

- **Push-to-talk and hands-free modes:** hold `Super+R` to dictate, or use a
  separate silence-ended hands-free action. JFlow is never always listening.
- **Noise-reduced PipeWire capture:** integrates with the existing EasyEffects
  virtual microphone, including its RNNoise and VAD input chain.
- **Durable recording and retry:** a WAV and job state are saved as you speak;
  retryable transcription failures retry once automatically and remain
  recoverable from the Library.
- **Accurate batch transcription:** the default release-only path uses
  ElevenLabs Scribe v2 and supports canonical vocabulary keyterms.
- **Local formatting:** recordings over 15 seconds can be cleaned, rephrased,
  and structured by Qwen3 running through Ollama on the laptop.
- **Safe Wayland delivery:** text is inserted only into the originally focused
  window. If that is no longer safe, the final text is copied for manual paste
  instead of being typed into the wrong application.
- **Minimal Quickshell UI:** a bottom-center status pill for recording and
  recovery, plus an on-demand JFlow Library for history, vocabulary, retries,
  and formatter inspection.
- **Private local history and audits:** recordings, transcripts, formatter
  prompts/responses, feedback, and learned aliases stay on the laptop under
  your user data directory.

## How a dictation moves through JFlow

```text
Super+R hold → PipeWire / EasyEffects → saved WAV job → Scribe v2 after release
                                                       ↓
                                 local Qwen formatting (>15 seconds, optional)
                                                       ↓
                                      safe wtype insertion into original app
```

If a cloud transcription step fails, the saved job is retried and remains
available in the Library. It is never discarded merely because the connection
or provider was unavailable.

Read [the implementation handoff](docs/IMPLEMENTATION.md) for the design,
reliability decisions, current setup, and next steps.

## Install

```bash
git clone git@github.com:JainamOswal18/JFlow.git ~/Code/JFlow
cd ~/Code/JFlow
go build -o ~/.local/bin/dictationd ./cmd/dictationd
dictationd init
```

Add `ELEVENLABS_API_KEY` to `~/.config/dictationd/credentials.env`, then copy
the provided user units and UI asset:

```bash
mkdir -p ~/.config/systemd/user ~/.local/share/dictationd/ui ~/.local/share/applications
cp packaging/systemd/dictationd*.service ~/.config/systemd/user/
cp ui/VoiceFlow.qml ~/.local/share/dictationd/ui/
cp ui/JFlow.qml ~/.local/share/dictationd/ui/
cp packaging/desktop/JFlow.desktop ~/.local/share/applications/
systemctl --user daemon-reload
systemctl --user enable --now dictationd.service dictationd-ui.service
```

After editing credentials, load them with:

```bash
systemctl --user restart dictationd.service
```

Add the two hold/release bindings from `integrations/hypr/keybinds.lua` to
`~/.config/hypr/custom/keybinds.lua`, replacing `YOUR_USER` with your Linux
username, then run `hyprctl reload`. The explicit path is required because
Hyprland may not include `~/.local/bin` in its environment.

For this machine, the service files, UI asset, config, and keybindings have
already been installed and the two user services are enabled. A fresh setup
should follow the commands above.

## Storage and privacy

- `~/.local/share/dictationd/jobs/<id>/audio.wav` is written **while** recording.
- Successful raw audio is deleted after one hour; failed audio is kept for 14 days.
- Job metadata and final transcript are kept locally for 30 days by default.
- Vocabulary corrections live in `~/.config/dictationd/vocabulary.json`.
- Secrets stay in `~/.config/dictationd/credentials.env` (mode `0600`) and are
  never stored in job files or logs.

## Commands

`dictationd toggle`, `dictationd handsfree-toggle`, `dictationd cancel`, `dictationd retry-last`, `dictationd dismiss-last`, `dictationd learn-selection`,
`dictationd history`, and `dictationd status` all communicate with the same
background daemon over a user-only Unix socket.

Typical use: hold `Super+R`, speak, then release it. Use `Super+Shift+V` to
retry the last failed job. Successful dictation leaves your clipboard unchanged.
If insertion is unsafe or fails, JFlow copies the saved final text to the
clipboard so it can be pasted into the intended field.

Every insertion gets at least 10 seconds; very long text receives a larger
character-based budget, capped at 30 seconds, before JFlow uses clipboard
recovery. When the recording target is LinkedIn, JFlow appends
`— Written using JFlow` after a blank line as the final content line. It is
added after transcription and formatting, so history, retries, insertion, and
clipboard recovery use the same text; it is never sent to Scribe or Qwen.

Press `Escape` to cancel an active recording; it is non-consuming while idle,
so applications still receive their normal Escape keypress. The bottom overlay
briefly offers Copy after every completed dictation and Retry after a failed
transcription.

`Super+Shift+Space` starts **hands-free** dictation: speak naturally, pause,
and JFlow stops after sustained silence. It is not always listening. A very
short accidental utterance is discarded, and `Escape` cancels safely.

## Automatic local formatting

JFlow formats recordings longer than 15 seconds after Scribe has returned its
clean transcript. It uses local Qwen3 1.7B through Ollama; no extra cloud API
key or ElevenLabs request is used. Qwen returns a compact block document:
paragraphs, headings, bullets, and numbered lists. JFlow validates and renders
those blocks itself, so numbering and plain-text syntax stay consistent rather
than being left to a free-form reply. General presentation guards discard an
invented standalone title, split only oversized prose blocks at sentence
boundaries, and turn an unambiguous ascending enumeration that Qwen leaves
inline into a numbered block. They contain no app, product, or phrase-specific
rules. Dictated text is source data to edit, never a question for Qwen to
answer. Qwen may remove fillers, correct grammar, and rephrase for clarity
without changing meaning. Each eligible job retains a local formatter audit:
the exact Ollama response body, model, effective system prompt, input text,
HTTP status, and end-to-end local request latency. It follows the same history
retention period as that job.

The formatter does not have special cases for phrases such as “Under the hood,”
specific applications, or calls to action. Its only deterministic structure
repair is generic: a sequence such as one/two/three or 1/2/3 must be ascending,
contain at least two non-empty items, and is then rendered as a numbered list.
Qwen still owns the wording and may improve it before that structure is drawn.

For mixed prose, an unambiguous ascending sequence is rendered as a numbered
section after its prose paragraphs. A preceding label becomes the ALL-CAPS
heading. It requires at least two consecutive, non-empty entries, so ordinary
prose and decimal numbers are left alone.

The formatter receives the transcript and, only when confidently inferred from
the active window, one short local context hint such as “AI-assistant request”,
“professional message”, or “casual message”. Raw window titles and process
arguments never go to the model. If context is uncertain, formatting is neutral.
The model is instructed to preserve meaning and requirements, rather than
answering, expanding, or adding new information to the request.

Install Ollama using its official installer, then start its system service:

```bash
curl -fsSL https://ollama.com/install.sh | sh
sudo systemctl enable --now ollama
ollama pull qwen3:1.7b
```

On this RTX 2050 system, confirm GPU use after the first formatting request:

```bash
ollama ps
```

The processor must show `100% GPU`. JFlow pre-warms the model in the background
after its service starts and keeps it warm for 15 minutes after use. If Ollama
is unavailable, GPU loading fails, or a request exceeds its local deadline,
JFlow inserts the unformatted Scribe result and marks that delivery in History.
The baseline is 10 seconds for short dictations, 30 seconds above 100 words,
and 60 seconds above 250 words; local Qwen uses no cloud credits while it runs.

When the active browser window's local title identifies LinkedIn, JFlow sends
the formatter a fixed LinkedIn-post style hint: a concise hook, short
professional first-person paragraphs, and an optional standalone reveal line.
The page title and URL are never sent to Ollama or any cloud service.

## JFlow Library

Run `dictationd library` or launch **JFlow** from your application launcher to
open the normal desktop window. It is separate from the recording overlay, so
closing it can never interrupt an active dictation. The Library lets you:

- Search local history by text, source app, status, or error.
- Copy a specific transcript, retry a failed recording, or delete an item.
- Add a canonical name or term once; JFlow sends that spelling to Scribe as a
  keyterm and learns local aliases from History corrections.
- Select a correction in any app and use **Learn selected correction** in the
  Library, or `Super+Alt+V`, to learn a close spelling/spacing alias without
  opening History. This is explicit only: JFlow never monitors your edits or
  clipboard in the background.
- Inspect formatter input, raw Qwen response, system prompt, latency, and
  final inserted text in the **Insights** tab. Mark an output Useful or Needs
  work; this local feedback is retained with the audit.

Canonical vocabulary terms are sent to ElevenLabs Scribe v2 as keyterms, which
adds ElevenLabs' keyterm surcharge to transcription requests. Learned aliases
stay in `~/.config/dictationd/vocabulary.json`, are applied locally after
transcription, and are never sent to ElevenLabs. Correct a saved History item
to let JFlow learn close spelling or spacing variants such as
`Jay Nam Oswal → Jainam Oswal`.

`Super+Shift+H` opens the Library directly. `Super+Alt+V` learns the currently
selected correction from the latest delivered dictation.

## Provider and cost visibility

The Insights tab reports recorded **cloud audio seconds**, not a guessed bill:
ElevenLabs allowances and pricing are account-specific and can change. It never
makes a second transcription request, changes your provider, or silently falls
back to a cloud provider. `elevenlabs_batch` and `sarvam` are counted as cloud;
`whisper_cli` is counted as local when you explicitly configure it.

## Local formatter evaluation

JFlow builds a private test set from formatter outputs you mark **Useful** and
History items you correct. It does not collect examples silently or upload them.
When you deliberately install another Ollama model, compare it locally with:

```bash
dictationd formatter-dataset
dictationd formatter-benchmark qwen3:1.7b llama3.2:1b
```

The benchmark is refused while JFlow is recording or processing. It reports
each model's output and latency for review; it never selects a model, changes
history, or uses any cloud transcription credits.

For a repeatable smoke test of the complete local formatter request/response
path (prompt, Ollama JSON contract, plan validation, and renderer), use only
synthetic text:

```bash
JFLOW_OLLAMA_E2E=1 JFLOW_OLLAMA_MODEL=qwen3:1.7b \
  go test ./internal/dictation -run '^TestOllamaFormatterE2E$' -v -count=1
```

This test is opt-in and never records audio, reads your history, or makes a
cloud transcription request. Use the same command with another installed model
name to compare it fairly before changing `formatter.model`.

## Sounds

```json
"sound": { "enabled": true }
```

The local start, stop, cancel, and permanent-failure sounds are controlled by
`sound.enabled`; they never send audio or data to a cloud service.

## Wake recovery

Hibernate or suspend can invalidate a Wayland client's connection. Install the
included system sleep hook once so both JFlow services restart automatically
after wake:

```bash
sudo install -D -m 0755 ~/Code/dictationd/scripts/dictationd-resume \
  /etc/systemd/system-sleep/dictationd-resume
```

It restarts only `dictationd.service` and `dictationd-ui.service` after a
completed suspend or hibernate; it does not run before sleep or alter power
settings.

Transient transcription failures retry automatically once after three seconds.
If both attempts fail, the local recording is retained for manual retry; the
bottom-center indicator shows `Retrying 1/2` while that retry runs.

## Providers

`elevenlabs_batch` is the stable default. It sends the saved WAV to Scribe v2
after you release the hotkey, so a network problem can never leave recording
stuck. `elevenlabs_realtime` remains experimental, while `sarvam` and
`whisper_cli` are supported by changing `asr.provider` in `config.json`.

For a compatible cleanup endpoint, enable `cleanup`, set its endpoint/model,
and add `LLM_API_KEY`. Cleanup failure never loses a transcription: raw ASR
text is delivered instead.

## Audio input

This setup defaults to `easyeffects_source`, the existing EasyEffects virtual
microphone on this machine. Its current input chain already has RNNoise and VAD
enabled. Leave that value in place unless you deliberately want raw microphone
audio; choose a different PipeWire source with `pactl list short sources`.

## Verification and troubleshooting

```bash
go test ./...
systemctl --user status dictationd.service dictationd-ui.service
dictationd status
dictationd history
journalctl --user -u dictationd.service -f
```

If the indicator or hotkey does not appear, reload Hyprland and restart the two
user services. See the handoff document for recovery behaviour and provider
configuration.
