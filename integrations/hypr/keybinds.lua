-- Add to ~/.config/hypr/custom/keybinds.lua. Use an absolute path because
-- graphical Hyprland sessions commonly omit ~/.local/bin from PATH.
hl.bind("SUPER + R", hl.dsp.exec_cmd("/home/YOUR_USER/.local/bin/dictationd start"),
    { description = "Voice: Hold to dictate" })
hl.bind("SUPER + R", hl.dsp.exec_cmd("/home/YOUR_USER/.local/bin/dictationd stop"),
    { release = true })

-- Optional recovery shortcut; Super+Shift+R is already your scratchpad.
hl.bind("SUPER + SHIFT + V", hl.dsp.exec_cmd("/home/YOUR_USER/.local/bin/dictationd retry-last"),
    { description = "Voice: Retry last dictation" })

-- Optional: open the searchable JFlow Library (history and vocabulary).
hl.bind("SUPER + SHIFT + H", hl.dsp.exec_cmd("/home/YOUR_USER/.local/bin/dictationd library"),
    { description = "Voice: Open JFlow Library" })
