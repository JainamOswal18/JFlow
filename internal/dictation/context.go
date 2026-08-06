package dictation

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// InferContextHint deliberately returns only a small, fixed vocabulary. Raw
// titles and command lines are inspected locally and are never sent to the
// formatter, since browser titles are both private and untrusted input.
func InferContextHint(target WindowTarget) string {
	meta := strings.ToLower(target.Class + " " + target.Title)
	switch {
	case isLinkedInTarget(target):
		return "Active style: LinkedIn post. Keep a confident first-person professional voice; use a short hook, 1 to 3 sentence paragraphs, and a standalone product/reveal line when natural. Use a list only for genuine takeaways; do not add claims or a call to action."
	case containsAny(meta, "chatgpt", "claude", "gemini", "perplexity", "copilot"):
		return "Active style: AI-assistant request. For multi-part requests, short ALL-CAPS headings, - or numbered lists, and **bold** are allowed when they improve clarity."
	case containsAny(meta, "thunderbird", "gmail", "outlook"):
		return "Active style: professional message. Keep an appropriate professional tone; use - or numbered lists and **bold** only when natural, never force structure on a short message."
	case containsAny(meta, "slack", "discord", "telegram", "whatsapp", "signal"):
		return "Active style: casual message. Preserve a relaxed tone; use a list only for genuine items or steps, with no headings."
	}
	if isTerminal(target.Class) && terminalRunsAssistant(target.PID) {
		return "Active style: AI-assistant request. For multi-part requests, short ALL-CAPS headings, - or numbered lists, and **bold** are allowed when they improve clarity."
	}
	return ""
}

func isLinkedInTarget(target WindowTarget) bool {
	return containsAny(strings.ToLower(target.Class+" "+target.Title), "linkedin")
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func isTerminal(class string) bool {
	class = strings.ToLower(class)
	return containsAny(class, "kitty", "foot", "alacritty", "wezterm", "terminal")
}

func terminalRunsAssistant(pid int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		candidate, err := strconv.Atoi(entry.Name())
		if err != nil || !processLooksLikeAssistant(candidate) {
			continue
		}
		if isDescendantOf(candidate, pid) {
			return true
		}
	}
	return false
}

func processLooksLikeAssistant(pid int) bool {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	args := strings.Split(string(b), "\x00")
	if len(args) == 0 || args[0] == "" {
		return false
	}
	name := strings.ToLower(filepath.Base(args[0]))
	return name == "claude" || name == "codex" || name == "aider" || name == "opencode"
}

func isDescendantOf(pid, root int) bool {
	for depth := 0; pid > 1 && depth < 8; depth++ {
		pid = parentPID(pid)
		if pid == root {
			return true
		}
	}
	return false
}

func parentPID(pid int) int {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			parent, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			return parent
		}
	}
	return 0
}
