# JFlow (`dictationd`)

A local-first, push-to-talk dictation service for Hyprland. It writes audio to
disk as you speak, streams it to Scribe v2 Realtime without rendering partial
text, optionally applies conservative cleanup, and inserts final text once.

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
mkdir -p ~/.config/systemd/user ~/.local/share/dictationd/ui
cp packaging/systemd/dictationd*.service ~/.config/systemd/user/
cp ui/VoiceFlow.qml ~/.local/share/dictationd/ui/
systemctl --user daemon-reload
systemctl --user enable --now dictationd.service dictationd-ui.service
```

After editing credentials, load them with:

```bash
systemctl --user restart dictationd.service
```

Add the two hold/release bindings from `integrations/hypr/keybinds.lua` to
`~/.config/hypr/custom/keybinds.lua`, then run `hyprctl reload`.

For this machine, the service files, UI asset, config, and keybindings have
already been installed and the two user services are enabled. A fresh setup
should follow the commands above.

## Storage and privacy

- `~/.local/share/dictationd/jobs/<id>/audio.wav` is written **while** recording.
- Successful raw audio is deleted after one hour; failed audio is kept for 14 days.
- Job metadata and final transcript are kept locally for 30 days by default.
- Secrets stay in `~/.config/dictationd/credentials.env` (mode `0600`) and are
  never stored in job files or logs.

## Commands

`dictationd toggle`, `dictationd cancel`, `dictationd retry-last`,
`dictationd history`, and `dictationd status` all communicate with the same
background daemon over a user-only Unix socket.

Typical use: hold `Super+R`, speak, then release it. Use `Super+Shift+V` to
retry the last failed job. If text cannot be inserted safely, it is copied to
the clipboard instead of being typed into the wrong window.

## Providers

`elevenlabs_realtime` is the default. It streams raw PCM as you speak but only
uses committed text after release. If that connection fails, the queued WAV is
sent to the ElevenLabs file endpoint on retry. `sarvam` and `whisper_cli` are
also supported by changing `asr.provider` in `config.json`.

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
