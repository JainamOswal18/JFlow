# Insertion Budget and LinkedIn Footer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent long Wayland insertions from timing out prematurely and append a final attribution line to LinkedIn dictations.

**Architecture:** Keep one `wtype` process and its existing clipboard recovery path. Make its timeout start at 10 seconds and scale for long text under a hard cap. Add a pure final-text helper that appends a LinkedIn-only footer after all formatting, before persistence and delivery.

**Tech Stack:** Go, existing JFlow daemon, `wtype`, Go unit and pipeline tests.

## Global Constraints

- The normal insertion budget is exactly 10 seconds.
- Longer text may scale beyond 10 seconds but is always bounded at 30 seconds.
- The footer is exactly `— Written using JFlow`, after two newlines, and only for LinkedIn targets.
- The footer is added once, after optional Qwen formatting; it must be present in insertion, history, retry, and clipboard recovery text.
- No footer text is included in Scribe or Ollama input.

### Task 1: Define and test final-text behavior

**Files:**
- Modify: `internal/dictation/formatter_test.go`
- Modify: `internal/dictation/daemon.go`

- [x] Add failing tests for `typingDeadline`: short text returns 10 seconds, a 1,201-rune post receives more than 10 seconds, and very long text caps at 30 seconds.
- [x] Add failing tests for `appendLinkedInFooter(text, WindowTarget)`: LinkedIn appends the footer after two newlines, a second call is unchanged, and a non-LinkedIn target is unchanged.
- [x] Run the focused tests and confirm they fail because the requested behavior is absent.
- [x] Implement the timeout rule and pure footer helper.
- [x] Re-run focused tests and confirm they pass.

### Task 2: Apply footer to durable delivery text

**Files:**
- Modify: `internal/dictation/daemon.go`
- Modify: `internal/dictation/pipeline_e2e_test.go`

- [x] Add a pipeline assertion that a LinkedIn job's final text ends in the footer before delivery.
- [x] Apply the footer immediately before assigning `job.FinalText`, after all formatting branches have finished.
- [x] Re-run the pipeline test and confirm it passes while non-LinkedIn behavior remains unchanged.

### Task 3: Verify and document

**Files:**
- Modify: `README.md`

- [x] Document the 10-second insertion floor, bounded long-text scaling, and the LinkedIn-only final footer.
- [ ] Run `go test ./... -count=1`, `go build ./cmd/dictationd`, and `git diff --check`.
- [ ] Install the binary, restart `dictationd.service`, and verify `dictationd status` reports `Ready`.
