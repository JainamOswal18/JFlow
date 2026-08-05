# JFlow (`dictationd`)

A local-first, push-to-talk dictation service for Hyprland. It writes audio to
disk as you speak, sends it to Scribe v2 when you release the hotkey, optionally
applies conservative cleanup, and inserts final text once.

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

`dictationd toggle`, `dictationd handsfree-toggle`, `dictationd cancel`, `dictationd retry-last`, `dictationd dismiss-last`,
`dictationd history`, and `dictationd status` all communicate with the same
background daemon over a user-only Unix socket.

Typical use: hold `Super+R`, speak, then release it. Use `Super+Shift+V` to
retry the last failed job. Every completed dictation is copied to the clipboard
before insertion, so it can always be pasted if the focused app has no editable
field or rejects simulated typing.

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
key or ElevenLabs request is used.

The formatter receives the transcript and, only when confidently inferred from
the active window, one short local context hint such as “AI-assistant request”,
“professional message”, or “casual message”. Raw window titles and process
arguments never go to the model. If context is uncertain, formatting is neutral.
The model is instructed to preserve meaning and requirements, rather than
answering, expanding, or rewriting the request.

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
is unavailable, GPU loading fails, a request exceeds seven
seconds, or the result does not safely preserve the original transcript, JFlow
inserts the unformatted Scribe result and marks that fallback in History.

## JFlow Library

Run `dictationd library` or launch **JFlow** from your application launcher to
open the normal desktop window. It is separate from the recording overlay, so
closing it can never interrupt an active dictation. The Library lets you:

- Search local history by text, source app, status, or error.
- Copy a specific transcript, retry a failed recording, or delete an item.
- Add a canonical name or term once; JFlow sends that spelling to Scribe as a
  keyterm and learns local aliases from History corrections.

Canonical vocabulary terms are sent to ElevenLabs Scribe v2 as keyterms, which
adds ElevenLabs' keyterm surcharge to transcription requests. Learned aliases
stay in `~/.config/dictationd/vocabulary.json`, are applied locally after
transcription, and are never sent to ElevenLabs. Correct a saved History item
to let JFlow learn close spelling or spacing variants such as
`Jay Nam Oswal → Jainam Oswal`.

The optional `Super+Shift+H` binding in
`integrations/hypr/keybinds.lua` opens the Library directly.

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
