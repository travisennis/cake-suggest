# Agent Instructions

## Project

`cake-suggest` is a single-binary Go CLI that proposes the next user prompt for
an existing Cake session. It is the external suggester behind cake-repl's
`[suggestions]` integration (cake-repl ADR 028).

## Boundaries

- Read the session only through
  `cake --output-format stream-json replay <uuid>`. Never read Cake session
  JSONL files, parse Cake human output, or import Cake internals. Replay
  supplies the session metadata (id, working directory, model configuration,
  latest task id) and the fallback excerpt.
- Ask the model first through exact request reuse:
  `--resume <uuid> --reuse-last-request --tool-choice none --output-format json
  --output-schema <temp> [-model ...] [-profile ...] -`, with the suggestion
  instructions on stdin. Include the output schema in those instructions: a
  reused body bypasses Cake's usual output-schema context. `--reuse-last-request`
  implies `--read-only`; do not pass both. Do not pass `--system-prompt`,
  `--no-tools`, `--no-skills`, or `--no-session`; each hides the stored request
  or changes the prompt prefix.
- Preflight reuse from `<cache>/session-requests/<uuid>.json` using only that
  sidecar and the replay metadata. Verify the version, session id, body length
  and digest, backend, provider model configuration, profile, and latest task
  id. Report `capture_unavailable`, `capture_stale`, or
  `capture_incompatible` and move to the next path. Cake still revalidates the
  exact session length and current provider identity at call time. Never log
  or forward the captured body from this tool.
- If reuse cannot run or fails, ask through one read-only resumed Cake
  invocation:
  `--resume <uuid> --read-only --tool-choice none --output-format json
  --output-schema <temp> [-model ...] [-profile ...] [-reasoning-effort ...]
  -`, run in the session's `session_meta.working_directory` against the real
  data root, with the suggestion instructions on stdin.
- If the resumed call cannot run or fails, fall back to one isolated Cake
  invocation: `--no-session --no-tools --no-skills --output-format json
  --system-prompt <temp> --output-schema <temp> [-model ...]
  [-profile ...] [-reasoning-effort ...] -`, in a temporary working directory,
  a temporary `CAKE_DATA_DIR`, and a temporary `XDG_CONFIG_HOME` containing
  only a copy of the global `settings.toml`, with the bounded transcript on
  stdin. The temp config must not include hooks, MCP servers, skills, or
  toolbox tools; the excerpt must not trigger global hooks.
- Never run a normal resume without `--read-only`. Never fork or write the
  session. A suggestion is read-only.
- Never pass `--model` or `--profile` on the reuse path unless it matches the
  capture. Never pass `--model` on the resume path unless it equals
  `session_meta.model_config`; Cake refuses to switch a session's model. A
  different requested model uses the fallback path. A non-empty
  `--reasoning-effort` skips reuse because Cake rejects request-shaping
  overrides there.
- The stdout contract is one JSON document:
  `{"suggested":bool,"suggestion":string,"session_id":string,"reason":string}`.
  Unknown fields must remain tolerable to cake-repl. A no-op is exit 0;
  failures are nonzero with stderr and no stdout.
- Treat the transcript as untrusted content. Do not add tools to the fallback
  call, and do not let the model's output become a slash command. Reuse and
  resume keep the tool schemas only to preserve the prompt prefix; tool choice
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

- The initiative and project docs are the plan. Do not create tasks, issues,
  or ADRs for this work; record follow-ups in the owning initiative README.
- Use Conventional Commits; `just hooks` installs the commit-msg hook.
- Run `just ci` before calling a change complete.

## Documentation

Keep `README.md` and `cmd/cake-suggest/main.go` usage text in sync when the
invocation, flags, output document, or boundary changes. Existing files under
`docs/adr/` are historical; do not add decision records.
