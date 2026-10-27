---
status: accepted
date: 2026-10-27
decision-makers: Travis Ennis
---
# Suggest through replay and an isolated Cake call

## Context and Problem Statement

`cake-suggest` needs the user/assistant exchange of an existing Cake session to
propose the next user prompt. The session is owned by Cake: its live format is
Cake's private JSONL, while the documented read-only view is
`cake --output-format stream-json replay <uuid>`. The suggester must also run a
model call without appending a turn, creating a fork, or racing Cake's session
lock.

cake-repl ADR 028 already chose an external `cake-suggest` process with one
JSON document as the boundary. This decision covers the suggester's internal
path: how it obtains the transcript and how it invokes a model.

## Decision Drivers

- Read-only: a suggestion must not change the session or create one.
- Stable boundary: do not depend on Cake's private session format or projector.
- Bounded cost and latency: the REPL invokes this after every completed turn.
- Testability without a real Cake binary or a real model call.
- Defensible behavior when the session is compacted, interrupted, or very
  large.

## Considered Options

- **Read the session JSONL directly.** Rejected: couples this tool to Cake's
  private record format, checkpoint projection, and trailing-record repair.
  The documented replay stream already supplies the conversation view.
- **Resume the session with a suggestion prompt.** Rejected: appends a turn to
  the user's real session and needs the session lock. A suggestion is not part
  of the conversation.
- **Fork the session, ask there, and discard the fork.** Rejected: creates a
  session, copies history, and pays for a full-context call just to obtain a
  short prompt.
- **Accept the visible transcript from cake-repl on stdin.** Rejected by
  cake-repl ADR 028: the timeline is a rendering view, not the session, and it
  would create a second transcript contract.
- **Replay the session, build a bounded transcript, and run one isolated
  no-session, no-tools Cake call (chosen).** It uses only documented commands,
  never mutates the session, and keeps the model call bounded.

## Decision Outcome

Chosen option: use `cake replay` for the read-only transcript, then one
isolated Cake completion over a bounded excerpt.

1. **Read.** `cake --output-format stream-json replay <uuid>` is run with the
   resolved Cake data root. Replay opens the session read-only (no lock, no
   append, no network). A replay error is a failure.
2. **Project.** The stream is decoded as documented NDJSON. This tool keeps
   `message` records with role `user` or `assistant`, `function_call` and
   `function_call_output` records, and `compaction.summary`; it ignores
   reasoning, usage, lifecycle, hook, and metadata records. Each block is
   clipped, the newest blocks fit a character budget, and one omission marker
   replaces older ones. The session id in `session_meta` must match the
   requested id.
3. **Ask.** A single Cake invocation runs in a temporary directory with a
   temporary `CAKE_DATA_DIR`:

   ```sh
   cake --no-session --no-tools --no-skills --output-format json \
     --system-prompt <temp> --output-schema <temp> [-model ...] [-profile ...] -
   ```

   The transcript is on stdin. The system prompt treats it as untrusted data,
   forbids using Cake's own runtime Developer message as session content, and
   asks for one user-voiced next prompt. The output schema requires
   `suggested` and permits `suggestion` and `reason`.
4. **Validate.** Missing `suggested`, malformed JSON, or `suggested:true` with
   an empty suggestion is a failure. A valid suggestion is collapsed to one
   line and bounded. The result document names the requested session id.
5. **Report.** Exit 0 emits one document. A deliberate no-op is
   `suggested:false` with a reason and is also exit 0. Operational failures
   exit nonzero with stderr and no stdout.

## Consequences

- Good: the session is never written, locked, resumed, or forked.
- Good: the interface depends only on documented Cake commands and the
  stream-json vocabulary.
- Good: the model call is isolated from project hooks, settings, skills, tools,
  and the user's session directory.
- Good: the transcript is bounded, so a huge session cannot make the
  suggestion call unbounded.
- Bad: the transcript is lossy. Reasoning, old context, and long tool output
  are omitted; a suggestion can miss a detail.
- Bad: it requires `--output-schema`, so an older Cake build fails the call
  instead of falling back to text parsing.
- Bad: it spends two processes and one metered model call per suggestion.
- Neutral: model quality is not proven here. This decision covers the
  boundary, not whether the suggested prompts are good.

## More Information

- cake-repl ADR 028 owns the REPL trigger, ghost rendering, `Ctrl+S` acceptance,
  dismissal, staleness, provenance, and failure policy.
- Cake `docs/integrations.md` owns the replay stream, session record semantics,
  and completion JSON.
- Cake's own `docs/integrations.md` "Session replay" section says replay is
  read-only and preserves record order; this tool relies on that contract.
