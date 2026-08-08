# Wayland Session Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make JFlow reliably regain its active Wayland environment on graphical login and after wake, while allowing the local formatter a realistic short-request deadline.

**Architecture:** Hyprland invokes a user-level session-ready script after exporting Wayland session variables. The script imports those variables into the user systemd manager and restarts JFlow. The existing privileged wake hook remains responsible only for wake-time restarts. Formatter deadline policy remains local and bounded.

**Tech Stack:** Bash, systemd user services, Hyprland Lua configuration, Go tests.

## Global Constraints

- Never type verification text into the active window or overwrite the clipboard.
- All test and operational shell commands use finite timeouts.
- Keep raw Scribe delivery on formatter failure; do not add formatter retries or a rewrite fallback.
- Preserve the existing root-level wake hook interface.

---

### Task 1: Add session-ready recovery script with a shell regression test

**Files:**
- Create: `scripts/dictationd-session-ready`
- Create: `scripts/dictationd-session-ready_test.sh`

**Interfaces:**
- Consumes: `WAYLAND_DISPLAY`, `XDG_CURRENT_DESKTOP`, `HYPRLAND_INSTANCE_SIGNATURE`.
- Produces: imported systemd user-manager environment and restarted `dictationd.service`, `dictationd-ui.service`.

- [ ] **Step 1: Write a failing shell test**

Create `scripts/dictationd-session-ready_test.sh` that supplies mock
`dbus-update-activation-environment` and `systemctl` executables through
`PATH`, runs the missing script with Wayland variables, and asserts the log
contains the exact activation import, systemd import, and service restart.

- [ ] **Step 2: Run the shell test and verify it fails because the script is absent**

Run: `timeout 10s bash scripts/dictationd-session-ready_test.sh`

Expected: non-zero status and a missing-script error.

- [ ] **Step 3: Implement the minimal session-ready script**

```bash
#!/usr/bin/env bash
set -euo pipefail
: "${WAYLAND_DISPLAY:?must run inside a Wayland session}"
dbus-update-activation-environment --systemd WAYLAND_DISPLAY XDG_CURRENT_DESKTOP HYPRLAND_INSTANCE_SIGNATURE
systemctl --user import-environment WAYLAND_DISPLAY XDG_CURRENT_DESKTOP HYPRLAND_INSTANCE_SIGNATURE
systemctl --user restart dictationd.service dictationd-ui.service
```

- [ ] **Step 4: Run the shell test again**

Run: `timeout 10s bash scripts/dictationd-session-ready_test.sh`

Expected: exit 0.

### Task 2: Wire session recovery into Hyprland and document installation

**Files:**
- Modify: `README.md`
- Modify: `/home/jainamop/.config/hypr/hyprland/execs.lua` (local installation)

**Interfaces:**
- Hyprland session startup invokes `~/.local/bin/dictationd-session-ready` after activation-environment setup.

- [ ] **Step 1: Document the Hyprland startup command**

```lua
hl.exec_cmd("$HOME/.local/bin/dictationd-session-ready")
```

- [ ] **Step 2: Install the script locally and update the active Hyprland configuration**

Copy the executable to `~/.local/bin` and add the command after the existing
`dbus-update-activation-environment --systemd` command.

- [ ] **Step 3: Reload Hyprland, then inspect the daemon environment**

Restart the services through the script and assert the daemon process contains
the same `WAYLAND_DISPLAY` as `systemctl --user show-environment`.

### Task 3: Raise and test the local formatter baseline deadline

**Files:**
- Modify: `internal/dictation/config.go`
- Modify: `internal/dictation/formatter.go`
- Modify: `internal/dictation/formatter_test.go`

**Interfaces:**
- `formatterDeadline(raw, cfg)` returns 15 seconds for short text when the configured timeout is absent; configured explicit values retain precedence.

- [ ] **Step 1: Update the deadline unit test to expect 15 seconds by default**

```go
if got := formatterDeadline("a short dictation", cfg); got != 15*time.Second {
    t.Fatalf("short deadline = %s, want 15s", got)
}
```

- [ ] **Step 2: Run the focused test and verify it fails with the existing 10-second baseline**

Run: `timeout 30s go test ./internal/dictation -run '^TestFormatterDeadlineScalesWithTranscriptSize$' -count=1`

Expected: assertion reports 10 seconds instead of 15.

- [ ] **Step 3: Set the default and zero-value fallback to 15 seconds**

Change `DefaultConfig().Formatter.TimeoutSecs` and the fallback in `formatterDeadline` from 10 to 15, retaining the 30- and 60-second scales for transcripts over 100 and 250 words.

- [ ] **Step 4: Run focused and full Go verification**

Run `timeout 30s go test ./internal/dictation -run '^TestFormatterDeadlineScalesWithTranscriptSize$' -count=1` and `timeout 60s go test ./...`.

### Task 4: Build, install, and perform bounded end-to-end checks

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Build the daemon**

Run: `timeout 30s go build -o ~/.local/bin/dictationd ./cmd/dictationd`

- [ ] **Step 2: Install the session-ready script and restart safely**

Run the script with the active Wayland environment. It restarts the daemon and indicator; it does not issue dictation commands.

- [ ] **Step 3: Verify the live daemon and formatter**

Check JFlow status, compare the live daemon's `WAYLAND_DISPLAY` with the user-manager environment, and run the opt-in local Ollama test.

- [ ] **Step 4: Commit and push the verified change directly to `main`**

Use a concise commit message and `timeout 30s git push origin main`.
