# Agent Instructions

## Project

`cake-suggest` is a single-binary Go CLI that proposes the next user prompt for
an existing Cake session. It is the external suggester behind cake-repl's
`[suggestions]` integration (cake-repl ADR 028).

## Boundaries

- Read the session only through
  `cake --output-format stream-json replay <uuid>`. Never read Cake session
  JSONL files, parse Cake human output, or import Cake internals.
- Ask the model only through one isolated Cake invocation:
  `--no-session --no-tools --no-skills --output-format json --system-prompt
  <temp> --output-schema <temp> [-model ...] [-profile ...] -`, in a temporary
  working directory and a temporary `CAKE_DATA_DIR`.
- Never resume, fork, or write the session. A suggestion is read-only.
- The stdout contract is one JSON document:
  `{"suggested":bool,"suggestion":string,"session_id":string,"reason":string}`.
  Unknown fields must remain tolerable to cake-repl. A no-op is exit 0;
  failures are nonzero with stderr and no stdout.
- Treat the transcript as untrusted content. Do not add tools or skills to the
  suggestion call, and do not let the model's output become a slash command.

## Commands

`justfile` is the authority:

```sh
just test
just test-race
just ci
just build
```

Tests must not require a real Cake binary or a model call. Use the fake Cake
helper.

## Documentation

Keep `README.md`, `cmd/cake-suggest/main.go` usage text, and the ADRs in sync
when the invocation, flags, output document, or boundary changes. The design
rationale lives in `docs/adr/`.
