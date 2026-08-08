# Wayland Session Recovery Design

## Goal

Keep JFlow's daemon connected to the active Hyprland Wayland session after a
fresh graphical login, suspend, or hibernate, so text insertion and clipboard
recovery continue to work without a manual restart.

## Cause

`dictationd.service` can start before Hyprland has imported
`WAYLAND_DISPLAY` into the user systemd manager. The daemon then has no
Wayland display in its environment. Both `wtype` and `wl-copy` subsequently
fail even though transcription succeeded. The existing root sleep hook only
restarts services after wake; it does not repair the fresh-login ordering.

## Design

Add a small, user-level `dictationd-session-ready` script. Hyprland runs it
immediately after it imports its activation environment. The script imports
the current `WAYLAND_DISPLAY`, `XDG_CURRENT_DESKTOP`, and
`HYPRLAND_INSTANCE_SIGNATURE` into the user systemd manager, then restarts
the daemon and indicator. Restarting is deliberate: a running service cannot
gain environment variables retroactively.

The existing `dictationd-resume` hook remains installed for wake events. It
restarts the same services and relies on the persistent user-manager
environment populated by the session-ready script.

For formatter responsiveness, retain the existing text-size deadline policy
but raise the default baseline from 10 to 15 seconds. A single short local
request may otherwise time out while Ollama is waking or briefly busy. This
does not add retries, cloud calls, or a rewrite fallback: unsuccessful
formatting still delivers the original Scribe text with its audit record.

## Error Handling

The session script exits with an explicit error when it is run outside a
Wayland session. It restarts both user services atomically through one
`systemctl --user restart` invocation. The pipeline continues to retain text
in history if desktop delivery cannot happen for another independent reason.

## Verification

- A shell regression test uses mock `dbus-update-activation-environment` and
  `systemctl` commands to confirm the exact import and restart sequence.
- Go tests lock in the 15-second baseline and the longer-transcript scaling.
- The full Go suite and an opt-in local Ollama formatter test run after build.
- Live verification compares the daemon process environment with the current
  user-manager Wayland environment, without sending keystrokes or changing
  clipboard contents.
