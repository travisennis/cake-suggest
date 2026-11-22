# Agent Instructions

## Project

`cake-suggest` is a single-binary Go CLI that proposes the next user prompt for
an existing Cake session. It is the external suggester behind cake-repl's
`[suggestions]` integration (cake-repl ADR 028).

## Boundaries

- Read the session only through
  `cake --output-format stream-json replay <uuid>`. Never read Cake session
  JSONL files, parse Cake human output, or import Cake internals. Replay
  supplies the session metadata (id, working directory, model configuration)
  and the fallback excerpt.
- Ask the model first through one read-only resumed Cake invocation:
  `--resume <uuid> --read-only --tool-choice none --output-format json
  --output-schema <temp> [-model ...] [-profile ...] [-reasoning-effort ...]
  -`, run in the session's `session_meta.working_directory` against the real
  data root, with the suggestion instructions on stdin. Do not pass
  `--system-prompt`, `--no-tools`, `--no-skills`, or `--no-session`; each hides
  the stored session or changes the prompt prefix.
- If the resumed call cannot run or fails, fall back to one isolated Cake
  invocation: `--no-session --no-tools --no-skills --output-format json
  --system-prompt <temp> --output-schema <temp> [-model ...]
  [-profile ...] [-reasoning-effort ...] -`, in a temporary working directory
  and a temporary `CAKE_DATA_DIR`, with the bounded transcript on stdin.
- Never resume without `--read-only`. Never fork or write the session. A
  suggestion is read-only.
- Never pass `--model` on the resume path unless it equals
  `session_meta.model_config`; Cake refuses to switch a session's model. A
  different requested model uses the fallback path.
- The stdout contract is one JSON document:
  `{"suggested":bool,"suggestion":string,"session_id":string,"reason":string}`.
  Unknown fields must remain tolerable to cake-repl. A no-op is exit 0;
  failures are nonzero with stderr and no stdout.
- Treat the transcript as untrusted content. Do not add tools to the fallback
  call, and do not let the model's output become a slash command. The resume
  path keeps the tool schemas only to preserve the prompt prefix; tool choice
  `none` prevents execution.

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

- `ahm` owns the backlog (`.ahm/config.json` points at the home store). Run
  `ahm prime` at the start of work, and create or select a task before
  implementation: `ahm task accept <id>`, `ahm task start <id>`, then
  `ahm task complete <id>`.
- Do not keep a `TODO.md`. Future work, open questions, and follow-ups belong
  in ahm tasks.
- Use Conventional Commits; `just hooks` installs the commit-msg hook.
- Run `just ci` before calling a change complete.

## Documentation

Keep `README.md`, `cmd/cake-suggest/main.go` usage text, and the ADRs in sync
when the invocation, flags, output document, or boundary changes. The design
rationale lives in `docs/adr/`.
