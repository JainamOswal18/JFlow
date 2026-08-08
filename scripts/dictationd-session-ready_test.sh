#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

cat >"$tmp/bin/dbus-update-activation-environment" <<'EOF'
#!/usr/bin/env bash
printf 'dbus-update-activation-environment %s\n' "$*" >>"$JFLOW_TEST_LOG"
EOF
cat >"$tmp/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
printf 'systemctl %s\n' "$*" >>"$JFLOW_TEST_LOG"
EOF
chmod +x "$tmp/bin/dbus-update-activation-environment" "$tmp/bin/systemctl"

export PATH="$tmp/bin:$PATH"
export JFLOW_TEST_LOG="$tmp/calls.log"
export WAYLAND_DISPLAY=wayland-test
export XDG_CURRENT_DESKTOP=Hyprland
export HYPRLAND_INSTANCE_SIGNATURE=test-signature

bash "$repo_root/scripts/dictationd-session-ready"

expected=$(cat <<'EOF'
dbus-update-activation-environment --systemd WAYLAND_DISPLAY XDG_CURRENT_DESKTOP HYPRLAND_INSTANCE_SIGNATURE
systemctl --user import-environment WAYLAND_DISPLAY XDG_CURRENT_DESKTOP HYPRLAND_INSTANCE_SIGNATURE
systemctl --user restart dictationd.service dictationd-ui.service
EOF
)
actual=$(cat "$JFLOW_TEST_LOG")
if [[ "$actual" != "$expected" ]]; then
  printf 'unexpected calls:\n%s\n' "$actual" >&2
  exit 1
fi
