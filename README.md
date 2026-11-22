# cake-suggest

An external prompt suggester for [Cake](https://github.com/travisennis/cake)
sessions. It reads an existing session through Cake's read-only replay stream,
asks Cake to resume that session read-only for one model call, and returns one
JSON document. If the resumed call cannot run, it uses a bounded replay excerpt
instead. It never writes to the session and never reads Cake session files.

This is the suggester side of the contract defined by cake-repl ADR 028
("Offer after-turn prompt suggestions from an external suggester"). cake-repl
runs it after a completed turn and shows a usable suggestion as ghost text in
the composer.

## Requirements

- A Cake build that supports:
  - `cake --output-format stream-json replay <uuid>`
  - `cake --resume <uuid> --read-only --tool-choice none --output-format json`
  - `--output-schema` on a normal Cake run
- Go 1.25 or newer to build.
- A model/profile configured for Cake. The resume path uses the session's own
  model configuration unless `-model` names the same entry; the fallback uses
  Cake's default or the requested model.

The resume path is optional at runtime. When the Cake build or the session
cannot support it, the bounded excerpt fallback runs instead. Replay and
`--output-schema` are required either way.

## Contract

cake-repl invokes exactly:

```sh
cake-suggest suggest -json [-cake-bin <path>] [-model <name>] [-profile <name>]
  [-reasoning-effort <effort>] -- <session-id>
```

The session argument follows `--` so it is never parsed as a flag. Every
optional empty value is omitted. The session id is a lowercase UUID.

Exit `0` with one JSON object on stdout:

```json
{"suggested":true,"suggestion":"Add a regression test for the retry path.","session_id":"01234567-89ab-4cde-8f01-23456789abcd"}
```

```json
{"suggested":false,"reason":"The work is complete.","session_id":"01234567-89ab-4cde-8f01-23456789abcd"}
```

`suggested:false` is a deliberate no-op, not a failure. Unknown fields are
ignored by cake-repl. A failure exits nonzero, writes a diagnostic to stderr,
and writes nothing to stdout. The suggestion is normalized to one line and at
most 300 characters before it is returned.

Without `-json`, the suggestion is printed to stdout; a no-op reason goes to
stderr.

## How it works

1. **Replay the session read-only.** Run
   `cake --output-format stream-json replay <uuid>`. This opens the session
   read-only: no lock, no append, no network. It supplies the session id,
   working directory, model configuration, and a bounded conversation excerpt.
2. **Resume the session read-only.** Run:

   ```sh
   cake --resume <uuid> --read-only --tool-choice none --output-format json \
     --output-schema <temp> [-model ...] [-profile ...] \
     [-reasoning-effort ...] -
   ```

   The call runs in `session_meta.working_directory` against the real Cake
   data root. The suggestion instructions and an end marker are on stdin as a
   final user message. `--read-only` suppresses session records, telemetry,
   hooks, and the writer lock, so the session is unchanged.
   `--tool-choice none` forbids tool calls while leaving the tool schemas in
   the request. The call does not pass `--system-prompt`, `--no-tools`,
   `--no-skills`, or `--no-session`: each would hide the stored history or
   change the prompt prefix.
3. **Fall back to the bounded excerpt when needed.** The resume path is
   skipped or abandoned when:

   - `session_meta` has no working directory, a relative working directory, or
     a directory that no longer exists;
   - `-model` names a different `[[models]]` entry from
     `session_meta.model_config` (Cake refuses a model switch while resuming);
   - the resumed Cake process fails for any reason, including a provider that
     rejects `--tool-choice` or `--read-only` on an older build;
   - the resumed call returns an unreadable or unusable suggestion document.

   The fallback builds a bounded transcript from the replay stream and runs one
   isolated Cake call in a temporary directory with a temporary data root:

   ```sh
   cake --no-session --no-tools --no-skills --output-format json \
     --system-prompt <temp> --output-schema <temp> \
     [-model ...] [-profile ...] [-reasoning-effort ...] -
   ```

   A valid `suggested:false` result does not trigger the fallback.
4. **Validate the one document.** The model must return
   `{"suggested":boolean,"suggestion":string,"reason":string}`. A missing
   `suggested`, an empty suggestion when `suggested` is true, or malformed JSON
   is a failure. The suggestion is reported with the requested session id.

### Why this shape

- **Replay, not session files.** The replay stream is a documented, read-only
  contract. Reading `~/.local/share/cake/sessions/*.jsonl` would couple this
  tool to Cake's record format and duplicate its projector. The stream also
  supplies the metadata the resume call needs.
- **Read-only resume, not a fork or a normal resume.** A normal resume appends
  a turn to the real session. A fork creates a session and copies history.
  `--read-only` restores the full stored conversation and the session identity
  without either side effect, so the model sees reasoning and tool output that
  the excerpt projector drops.
- **`--tool-choice none`, not `--no-tools`.** Removing tools changes the
  request's tool schemas and prompt prefix. Keeping them and forbidding calls
  preserves the prefix a cache-aware provider can reuse while preventing tool
  execution.
- **The session's working directory, not a temporary one.** Cake rebuilds its
  project prompt from the current directory. Running where the session ran
  keeps project instructions, settings, and skills close to what the session
  used. Read-only mode keeps hooks out of the request.
- **A fallback, not a replacement.** Older Cake builds, deleted project
  directories, and model overrides must still produce a suggestion. The bounded
  excerpt remains the compatibility path.

## Usage

```sh
# The shape cake-repl uses.
cake-suggest suggest -json -- 01234567-89ab-4cde-8f01-23456789abcd

# Human-readable output.
cake-suggest suggest -- 01234567-89ab-4cde-8f01-23456789abcd

# Use a specific profile/effort and show content-free timing/cost diagnostics.
cake-suggest suggest -json -verbose \
  -profile concise -reasoning-effort low -- <session-id>

# Point at a different Cake build or data root.
cake-suggest suggest -json -cake-bin /path/to/cake \
  -data-dir /path/to/cake-data -- <session-id>
```

### Enabling it in cake-repl

In trusted cake-repl config (`~/.config/cake-repl/config.toml` or an explicit
`-config`):

```toml
[suggestions]
enabled = true
cake-suggest-bin = "cake-suggest"
```

Prompt suggestions are off by default. The automatically loaded project-local
`.cake-repl.toml` may only disable them; it cannot enable them or choose the
binary. `-no-session` disables them.

## Version

`cake-suggest -version` (also `version`, `-V`, and `--version`) prints the
binary version followed by the embedded VCS revision and commit time:

```text
cake-suggest 0.1.0
revision 92510da...
time 2026-10-28T18:45:10Z
```

The revision comes from Go's embedded build information. After `just install`,
this output shows which commit produced the installed binary; a dirty worktree
is marked `(modified)`. `just ci` builds the binary and asserts that its version
output contains a revision line.

## Flags

| Flag | Meaning |
| --- | --- |
| `-cake-bin <path>` | Cake executable. Default `$CAKE_BIN`, then `cake` on `PATH`. |
| `-data-dir <path>` | Cake data root for replay and resume. Default `$CAKE_DATA_DIR`, then `~/.local/share/cake`. |
| `-model <name>` | Model for the suggestion call. Empty uses the session's model configuration on the resume path and Cake's default on the fallback. A name that differs from `session_meta.model_config` skips the resume path so the requested model can be used. |
| `-profile <name>` | Profile for the suggestion call. Empty omits the flag. |
| `-reasoning-effort <effort>` | Reasoning effort for the suggestion call: `none`, `low`, `medium`, `high`, `xhigh`, or `max`. Empty omits the flag. |
| `-timeout <duration>` | Whole-request deadline, including replay and either model path. Default `50s`. cake-repl allows 60s. |
| `-max-transcript-chars <n>` | Bound for the fallback transcript excerpt. Default `20000`; raise it for a session that needs more context. |
| `-max-replay-bytes <n>` | Replay stream bound. Default `67108864` (64 MiB). |
| `-json` | Emit one JSON document. |
| `-verbose` | Print content-free timings, entry counts, token usage, the context mode (`resume`, `excerpt`, or `none`), and a bounded fallback reason to stderr. |

## Security and failure behavior

- The resume call is read-only: no session records, telemetry, hooks, or writer
  lock. The fallback runs without a session, tools, skills, project settings,
  or the user's data root.
- The resume call runs in the session's working directory, so it loads that
  project's trusted Cake configuration and instruction files, as a normal Cake
  run there would. Read-only mode still suppresses hooks, and tool choice
  `none` prevents tool execution.
- `--tool-choice none` prevents tool execution even though the tool schemas
  remain in the request. The tool set is kept only to preserve the prompt
  prefix.
- The transcript is sent only to the model endpoint selected by the user's Cake
  configuration. The resumed call sends the session's stored conversation; the
  fallback sends the bounded excerpt. Neither is written to a persistent file
  by this tool.
- The fallback's temporary prompt and schema files are created with owner-only
  permissions and removed afterward. The resume path writes only its temporary
  schema file.
- `-verbose` and the debug log contract in cake-repl never include transcript
  content, only counts, timings, token usage, the mode, and a bounded fallback
  reason.
- Replay, model, schema, and output failures are nonzero exits with a bounded
  stderr diagnostic. cake-repl surfaces at most one warning per process.
- A suggestion is untrusted model output. cake-repl bounds it, shows it as
  ghost text, and never parses an accepted suggestion as a slash command.

## Limitations

- The resume path sends the full stored conversation, so its cost grows with
  the session. The whole-request timeout bounds latency, not provider spend.
  Provider prompt caching is not guaranteed; only the request prefix and
  session identity are preserved.
- The fallback is lossy: reasoning is omitted, tool output is clipped, and old
  context is dropped. A suggestion can miss a detail that the full session
  would supply.
- The suggestion call is metered after every completed turn. The fallback's
  transcript bound keeps it small; the resume path depends on the session's
  existing context size.
- Model suggestion quality is not evaluated here. The project proves the
  contract, the subprocess behavior, and the document shape; it does not claim
  that suggestions are useful for every session.
- The whole-request timeout covers replay and both model attempts. A resume
  that consumes the deadline leaves no time for the fallback, so the run
  reports a timeout instead of an excerpt suggestion.
- The suggestion is not editable in place. cake-repl accepts it with `Ctrl+S`
  or discards it when the user types; editing one word means accepting it
  first or typing a new prompt.
- `--output-schema` is required. An older Cake build without it fails the
  suggestion call rather than falling back to unvalidated text.

## Build and test

```sh
just hooks          # install the commit-msg hook (Conventional Commits)
just build          # bin/cake-suggest
just install        # go install ./cmd/cake-suggest
cake-suggest -version  # confirm the installed revision
just test
just test-race
just ci             # fmt-check, vet, test, build, revision check
```

Tests use a fake Cake process for replay, the read-only resume call, and the
fallback call. They require no real Cake binary and no model call.

## Verified

- `just ci` and `go test -race ./...` pass.
- Against a real Cake build and a mock provider, a suggestion ran on the resume
  path in the session's working directory with `tool_choice: none`, kept the
  session id header and the full tool list, returned the JSON document, and
  left the session file and telemetry sidecar unchanged.
- Tests cover the resume arguments and working directory, each skip and
  fallback trigger, the excerpt call, and the no-op path.
- The earlier excerpt-only verification still holds for the fallback: replay of
  a 487-entry session parsed into a 20,100-character excerpt, and one real
  suggestion call returned a valid one-document result.
- Driven cake-repl in a 100x30 tmux pane against the checked-in fake cake and
  this real binary: the ghost `Add a focused test for the fake path.` appeared
  with the `Ctrl+S use suggestion` hint, and `Ctrl+S` submitted it as a `YOU`
  row.

## Related

- [ADR 001](docs/adr/001-suggest-through-replay-and-an-isolated-cake-call.md):
  why replay and the isolated excerpt call exist.
- [ADR 002](docs/adr/002-resume-read-only-for-suggestions.md): why the primary
  path is a read-only resume and when the excerpt fallback runs.
- cake-repl ADR 028: the REPL-side trigger, apply, accept, dismiss, and
  provenance contract.
