-- Run after Hyprland has established its Wayland environment. This restarts
-- JFlow so wtype and wl-copy inherit the active compositor connection.
hl.exec_cmd("$HOME/.local/bin/dictationd-session-ready")
