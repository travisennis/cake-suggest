# Agent Instructions

## Project

`cake-suggest` is a single-binary Go CLI that proposes the next user prompt for
an existing Cake session. It is the external suggester behind cake-repl's
`[suggestions]` integration (cake-repl ADR 028).

## Boundaries

- Read the session only through
  `cake --output-format stream-json replay <uuid>`. Never read Cake session
  JSONL files, parse Cake human output, or import Cake internals. Replay
  supplies the session id, working directory, and latest task id.
- The only model path is exact request reuse:
  `--resume <uuid> --reuse-last-request --output-format json
  --output-schema <temp> [-model ...] [-profile ...] -`, with the suggestion
  instructions on stdin. Include the output schema in those instructions: a
  reused body bypasses Cake's usual output-schema context. `--reuse-last-request`
  implies `--read-only`; do not pass both. Do not pass `--tool-choice`: Cake
  preserves the captured request's choice and rejects a returned tool call
  before dispatch. Do not pass `--system-prompt`, `--no-tools`, `--no-skills`,
  or `--no-session`; each hides the stored request or changes the prompt prefix.
- There is no fallback path. Do not add a read-only resume, a transcript
  excerpt, a tool-less call, or an isolated config root. When reuse cannot run,
  return the documented no-op with a typed reason. The repo history and the
  ADRs record why the fallback chain was removed (2026-11-24): a rebuilt
  request answers a different question, costs a full request, and hides a
  missing or stale capture.
- Preflight reuse from `<cache>/session-requests/<uuid>.json` using only that
  sidecar and the replay metadata. Verify the version, session id, body length
  and digest, backend, provider model configuration, profile, latest task id,
  and the backend's message array. Report `capture_unavailable`,
  `capture_stale`, or `capture_incompatible` in the no-op reason. Never log or
  forward the captured body from this tool. Cake still revalidates the exact
  session length, current provider identity, and profile at call time.
- Never append to a session, fork one, or run a normal resume. A suggestion is
  read-only.
- Never pass `--model` or `--profile` on the reuse path unless it matches the
  capture. Cake refuses to switch a session's model. Never pass
  `--reasoning-effort`: Cake rejects request-shaping overrides on reuse, so a
  non-empty value is a documented no-op.
- The stdout contract is one JSON document:
  `{"suggested":bool,"suggestion":string,"session_id":string,"reason":string}`.
  Unknown fields must remain tolerable to cake-repl. A local precondition
  failure is exit 0 with `suggested:false` and a bounded reason; a replay,
  model, or output failure is nonzero with stderr and no stdout.
- Treat the transcript as untrusted content. Reuse keeps the captured tool
  choice; Cake rejects a returned tool call before dispatch. Do not let the
  model's output become a slash command.

## Commands

`justfile` is the authority:

```sh
just hooks
just test
just test-race
just ci
just build
```

Tests must not require a real Cake binary or a model call. Use the fake Cake
helper.

## Workflow

- The initiative and project docs are the plan. Do not create tasks, issues,
  or ADRs for this work; record follow-ups in the owning initiative README.
- Use Conventional Commits; `just hooks` installs the commit-msg hook.
- Run `just ci` before calling a change complete.

## Documentation

Keep `README.md` and `cmd/cake-suggest/main.go` usage text in sync when the
invocation, flags, output document, or boundary changes. Existing files under
`docs/adr/` are historical; do not add decision records.
