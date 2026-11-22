---
status: accepted
date: 2026-11-22
decision-makers: Travis Ennis
---
# Resume read-only for suggestions

## Context and Problem Statement

ADR 001 chose a bounded replay excerpt plus one isolated no-session
completion. That keeps the session untouched, but it drops reasoning, long
tool output, and older context, and it starts a fresh provider conversation
that cannot reuse the session's identity or prompt cache.

Cake now supports `--read-only` on `--resume` and `--tool-choice`. A read-only
resume rebuilds the stored conversation, keeps the session UUID for provider
session headers, and writes no records, telemetry, or hooks. `cake-suggest`
should use it as the primary suggestion path while keeping the excerpt call as
a compatibility and failure fallback.

## Decision Drivers

- Full session context without mutating the session.
- Session identity and prompt-prefix reuse for providers that cache.
- No tool execution.
- A bounded, predictable fallback when the resume cannot run.
- Compatibility with older Cake builds and deleted project directories.
- Testability without a real Cake binary or a model call.

## Considered Options

- **Keep the excerpt-only path.** Rejected: it is lossy, and it cannot reuse
  the session's provider conversation or cache.
- **Resume normally.** Rejected: appends a suggestion turn to the real session.
- **Fork and ask.** Rejected: creates a session and copies history.
- **Resume read-only with `--no-tools`.** Rejected for the primary path:
  removing tools changes the request schemas and prompt prefix. It remains the
  fallback's shape, where isolation matters more than prefix parity.
- **Resume read-only with `--tool-choice none` (chosen).** It restores the full
  history, keeps the tool schemas in the request, and forbids tool calls.
- **Replace the fallback.** Rejected: older Cake builds, model overrides, and
  missing project directories still need a path.

## Decision Outcome

Chosen option: replay for metadata and a bounded excerpt, then a read-only
resume with tool choice `none`; fall back to the isolated excerpt call when the
resume cannot run.

1. **Read.** `cake --output-format stream-json replay <uuid>` supplies the
   session id, working directory, model configuration, and the bounded excerpt.
   A replay error is a failure.
2. **Resume.** The primary call is
   `cake --resume <uuid> --read-only --tool-choice none --output-format json
   --output-schema <temp> [-model ...] [-profile ...]
   [-reasoning-effort ...] -`. It runs in `session_meta.working_directory`
   against the resolved real data root. The suggester instructions and an end
   marker go to stdin as a final user message. No `--system-prompt`,
   `--no-tools`, `--no-skills`, or `--no-session` is passed.
3. **Fallback.** The excerpt path runs when the session has no usable working
   directory, the requested `-model` differs from `session_meta.model_config`,
   the resumed process fails, or the resumed document is unusable. A valid
   `suggested:false` is not a fallback.
4. **Validate.** Both paths must return one document with a boolean
   `suggested`; `suggested:true` requires a non-empty suggestion. A suggestion
   is normalized to one line and bounded. A no-op is exit 0.
5. **Report.** Exit 0 emits one document. Operational failures exit nonzero
   with stderr and no stdout.

## Consequences

- Good: the model sees the full stored conversation and the session identity
  without a write, lock, hook, or telemetry side effect.
- Good: keeping the tool schemas with `tool_choice none` preserves the request
  prefix for providers that cache across requests.
- Good: the excerpt fallback keeps older Cake builds and unusual sessions
  working.
- Bad: the resume path's input grows with the session, so its provider cost is
  less predictable than the excerpt's fixed bound.
- Bad: a provider that ignores `tool_choice` could attempt tool calls; Cake's
  read-only run still cannot persist anything, and the suggestion is not
  trusted as a command.
- Bad: Cake does not attach the provider's native output-schema constraint
  while tools are enabled. The schema is delivered as context and Cake
  validates the final message, so correction turns are possible.
- Neutral: provider prompt caching is not guaranteed. Only the request prefix
  and session identity are preserved.
- Neutral: suggestion quality is not proven here. This decision covers the
  context source and its fallback, not whether the prompts are useful.

## More Information

- ADR 001 remains the decision for replay and the isolated excerpt call. This
  ADR changes which path is primary and why.
- Cake `docs/integrations.md` owns the read-only resume contract and the
  `--tool-choice` request field. Cake commit `3a69640` added both flags.
- cake-repl ADR 028 owns the REPL trigger, ghost rendering, acceptance,
  dismissal, staleness, provenance, and failure policy.
