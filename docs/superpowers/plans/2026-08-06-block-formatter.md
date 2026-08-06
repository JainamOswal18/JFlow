# Block Formatter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace phrase-specific formatting rules with a general Qwen block document contract.

**Architecture:** Qwen returns a strict JSON document containing paragraph, heading, bullet-list, and numbered-list blocks. JFlow validates and renders that document, with only phrase-agnostic presentation guards for oversized paragraphs and unambiguous ascending enumerations. Raw-Scribe delivery on formatter failure remains unchanged.

**Tech Stack:** Go, local Ollama/Qwen3 1.7B, strict JSON Schema, Go unit and opt-in local-model E2E tests.

## Global Constraints

- Only the Scribe transcript and a fixed local context hint reach Ollama; titles, URLs, audio, and credentials stay local.
- Preserve facts, names, numbers, tone, and grammatical person. Do not invent content.
- Render plain text only: uppercase headings, `-` bullets, `1.` numbers, and no code fences.
- Keep formatter eligibility, local execution, audit logging, and raw-Scribe-on-formatter-error behavior unchanged.
- Do not use phrase-specific structure detection.

### Task 1: Add a general document renderer

**Files:** modify `internal/dictation/formatter.go`; modify `internal/dictation/formatter_test.go`.

**Interface:**

```go
type formatterBlock struct {
    Type  string   `json:"type"`
    Text  string   `json:"text,omitempty"`
    Items []string `json:"items,omitempty"`
}
type formatterDocument struct {
    Blocks []formatterBlock `json:"blocks"`
}
```

- [x] Write `TestRenderFormatterDocument` with the current post represented as paragraph → heading → numbered items → paragraph → closing paragraph. Expect exact visible text with blank lines.
- [x] Implement validation: non-empty document, known types only, text-only paragraph/heading, at least two non-empty items for lists. Render blocks separated by a blank line; uppercase headings; prefix bullets and numbers deterministically.
- [x] Remove `extractInlineNumberedList`, `extractInlineSpokenNumberedList`, and phrase-specific closing-line logic.
- [x] Re-run `go test ./internal/dictation -run '^TestRenderFormatterDocument$' -count=1`; it passes.

### Task 2: Switch Qwen to the block schema

**Files:** modify `internal/dictation/formatter.go`; modify `internal/dictation/formatter_test.go`.

- [x] Change `TestFormatWithOllamaUsesSafeLocalPayload` to require a `blocks` JSON-Schema property and reject the old `layout`, `content`, and `break_after` properties.
- [x] Replace the formatter instruction with general semantic block rules and require JSON only, with no Markdown markers inside text/items.
- [x] Update `FormatWithOllama` to send and decode the strict block schema, then invoke the document renderer and phrase-agnostic presentation guards.
- [x] Re-run the payload test; it passes.

### Task 3: Test real local-model behavior and document it

**Files:** modify `internal/dictation/formatter_ollama_e2e_test.go`; modify `README.md`; modify `docs/IMPLEMENTATION.md`.

- [x] Add four local-only E2E fixtures: the current LinkedIn post; a three-step AI request; a two-paragraph casual message; and a professional email with unordered requirements.
- [x] Each fixture asserts required phrases, expected structural markers where the source is explicitly ordered, and no code fences or wrapper leakage.
- [x] Run `JFLOW_OLLAMA_E2E=1 go test ./internal/dictation -run '^TestOllamaFormatterE2E$' -count=1 -v` and inspect raw responses plus rendered output.
- [x] Run `go test ./... -count=1 && go build ./cmd/dictationd && git diff --check`.
- [x] Document the block contract and the narrow grammar-based presentation guards.
