# Insertion Budget and LinkedIn Footer Design

## Goal

Prevent long, otherwise successful dictations from failing during Wayland text
insertion, and append an attribution line to content dictated into LinkedIn.

## Evidence

Job `28bceaba762f57ca7efe45e7` completed Scribe and local formatting, then
failed only at insertion with `wtype timed out after 4s`. Its 55-second
LinkedIn post was copied to the clipboard intact. The phrase “Hold a key,
speak, release” was part of the Scribe transcript, not added by JFlow.

## Design

### Insertion deadline

- Every `wtype` insertion receives at least 10 seconds.
- Text longer than 10 seconds' normal insertion budget scales by character
  count, retaining a hard upper bound so a stalled Wayland client cannot block
  JFlow indefinitely.
- The existing single `wtype` process and clipboard recovery path remain
  unchanged.

### LinkedIn footer

- Determine LinkedIn solely from the locally captured recording target, using
  the same local context classification already used for style hints.
- After transcription and optional formatting are complete, append exactly
  `— Written using JFlow` after two newlines.
- The footer is applied only for a LinkedIn target and only once. It is part of
  `FinalText`, so insertion, history, retry, and clipboard recovery all use the
  exact same content.
- The footer is never sent to Scribe or Qwen and never added for any other
  application.

## Tests

- Unit-test the 10-second minimum, scaling behavior, and hard cap.
- Unit-test footer placement, idempotence, and non-LinkedIn no-op behavior.
- Extend the durable pipeline test to assert the LinkedIn final text contains
  the footer at its final line.
- Run focused tests, the full Go suite, a build, and `git diff --check`.
