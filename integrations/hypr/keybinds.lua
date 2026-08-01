-- Add to ~/.config/hypr/custom/keybinds.lua. Super+R is currently unused.
hl.bind("SUPER + R", hl.dsp.exec_cmd("dictationd start"),
    { description = "Voice: Hold to dictate" })
hl.bind("SUPER + R", hl.dsp.exec_cmd("dictationd stop"),
    { release = true })

-- Optional recovery shortcut; Super+Shift+R is already your scratchpad.
hl.bind("SUPER + SHIFT + V", hl.dsp.exec_cmd("dictationd retry-last"),
    { description = "Voice: Retry last dictation" })
