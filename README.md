# cake-suggest

An external prompt suggester for [Cake](https://github.com/travisennis/cake)
sessions. It reads an existing session through Cake's read-only replay stream
and first asks Cake to reuse the exact provider request captured after the
completed turn. If that cannot run, it asks Cake to resume the session
read-only. If neither can run, it uses a bounded replay excerpt. It returns one
JSON document, never writes to the session, and never reads Cake session files.

This is the suggester side of the contract defined by cake-repl ADR 028
("Offer after-turn prompt suggestions from an external suggester"). cake-repl
runs it after a completed turn and shows a usable suggestion as ghost text in
the composer.

## Requirements

- A Cake build that supports:
  - `cake --output-format stream-json replay <uuid>`
  - `cake --resume <uuid> --reuse-last-request --output-format json`
  - `cake --resume <uuid> --read-only --tool-choice none --output-format json`
  - `--output-schema` on a normal Cake run
- Go 1.25 or newer to build.
- A model/profile configured for Cake. Reuse uses the captured request's model
  configuration when `-model` is empty. Resume uses the session's own model
  configuration unless `-model` names the same entry. The excerpt fallback
  uses Cake's default or the requested model.

The reuse and resume paths are optional at runtime. When the Cake build or the
session cannot support reuse, the read-only resume runs; when resume cannot
run, the bounded excerpt fallback runs. Replay and `--output-schema` are
required either way.

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
   working directory, model configuration, latest task id, and a bounded
   conversation excerpt.
2. **Reuse the captured request.** Run:

   ```sh
   cake --resume <uuid> --reuse-last-request \
     --output-format json --output-schema <temp> [-model ...] [-profile ...] -
   ```

   `--reuse-last-request` implies `--read-only`, so this tool does not pass
   both. It also does not pass `--tool-choice`: Cake preserves the captured
   request's choice and rejects a returned tool call before dispatch. Cake
   sends the next-request body captured after the latest successful persisted
   task, appends the suggestion instructions as the final user message, and
   keeps the captured `tool_choice`. The captured system prompt, tool schemas,
   history, and request-shaped hook context stay intact, so a cache-aware
   provider can reuse the prefix. Because the reused body bypasses Cake's
   usual output-schema context, the appended instructions also include the
   JSON schema. The call writes no session records, telemetry, hooks, or writer
   lock and does not read the current project prompt.

   Before the call, `cake-suggest` preflights
   `<cache>/session-requests/<uuid>.json` against the replay metadata. It
   verifies the capture version, session id, body length and digest, backend,
   provider model configuration, profile, and whether the capture belongs to
   the session's latest task. Local failures are reported as
   `capture_unavailable`, `capture_stale`, or `capture_incompatible` and skip
   to the next path. Cake revalidates the exact session length, current
   provider identity, and profile before sending.

   Reuse is skipped when `-reasoning-effort` is set because Cake rejects
   request-shaping overrides on this path, or when `-model`/`-profile` do not
   match the capture. The child runs in `session_meta.working_directory` when
   that directory exists; otherwise reuse can still run because it bypasses
   project context.
3. **Resume the session read-only.** If reuse is unavailable or fails, run:

   ```sh
   cake --resume <uuid> --read-only --tool-choice none --output-format json \
     --output-schema <temp> [-model ...] [-profile ...] \
     [-reasoning-effort ...] -
   ```

   The call runs in `session_meta.working_directory` against the real Cake
   data root. The suggestion instructions and an end marker are on stdin as a
   final user message. `--read-only` suppresses session records, telemetry,
   hooks, and the writer lock, so the session is unchanged.
   `--tool-choice none` asks the provider to forbid tool calls while leaving
   the tool schemas in the request. The call does not pass `--system-prompt`,
   `--no-tools`, `--no-skills`, or `--no-session`: each would hide the stored
   history or change the prompt prefix. Resume is skipped when:

   - `session_meta` has no working directory, a relative working directory, or
     a directory that no longer exists;
   - `-model` names a different `[[models]]` entry from
     `session_meta.model_config` (Cake refuses a model switch while resuming);
   - the resumed call fails or returns an unusable suggestion document.
4. **Fall back to the bounded excerpt when needed.** When both reuse and
   resume are unavailable or fail, build a bounded transcript from the replay
   stream and run one isolated Cake call in a temporary directory with a
   temporary data root and a temporary Cake config root. The temp config
   contains only a copy of the user's global `settings.toml`, so model and
   profile selection still resolve while global hooks, MCP servers, skills,
   and toolbox tools do not load:

   ```sh
   cake --no-session --no-tools --no-skills --output-format json \
     --system-prompt <temp> --output-schema <temp> \
     [-model ...] [-profile ...] [-reasoning-effort ...] -
   ```

   A valid `suggested:false` result from any path is a deliberate no-op and
   does not trigger the next fallback.
5. **Validate the one document.** The model must return
   `{"suggested":boolean,"suggestion":string,"reason":string}`. A missing
   `suggested`, an empty suggestion when `suggested` is true, or malformed JSON
   is a failure. The suggestion is reported with the requested session id.

### Why this shape

- **Replay, not session files.** The replay stream is a documented, read-only
  contract. Reading `~/.local/share/cake/sessions/*.jsonl` would couple this
  tool to Cake's record format and duplicate its projector. The stream also
  supplies the metadata the reuse preflight and resume call need.
- **Exact request reuse, not semantic reconstruction.** A normal resume
  rebuilds the system prompt, project context, skills, MCP catalog, and tools
  from the current invocation. The captured body already contains the request
  the completed turn ended with, so reusing it preserves the provider prefix
  and gives prompt caching its best chance without changing the session.
- **Read-only resume, not a fork or a normal resume.** If reuse cannot run, a
  normal resume would append a turn and a fork would create and copy a session.
  `--read-only` restores the full stored conversation and the session identity
  without either side effect, so the model sees reasoning and tool output that
  the excerpt projector drops.
- **Preserve the captured tool choice; use `--tool-choice none` on resume.**
  Removing tools changes the request's tool schemas and prompt prefix. Reuse
  keeps the captured choice and rejects any returned tool call before dispatch.
  The read-only resume keeps the schemas and passes `none` to ask the provider
  not to call a tool.
- **The session's working directory, not a temporary one.** For resume, Cake
  rebuilds its project prompt from the current directory; running where the
  session ran keeps project instructions, settings, and skills close to what
  the session used. Reuse keeps the captured context instead, and uses that
  directory only when it still exists.
- **A fallback chain, not a replacement.** Older Cake builds, missing or stale
  captures, deleted project directories, and model overrides must still
  produce a suggestion. The bounded excerpt remains the last compatibility
  path.

## Usage

```sh
# The shape cake-repl uses.
cake-suggest suggest -json -- 01234567-89ab-4cde-8f01-23456789abcd

# Human-readable output.
cake-suggest suggest -- 01234567-89ab-4cde-8f01-23456789abcd

# A non-empty effort skips reuse and starts at the read-only resume path.
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
| `-data-dir <path>` | Cake data root for replay, reuse, and resume. When set, Cake also uses it as the cache root for the request-capture sidecar. Default `$CAKE_DATA_DIR`, then Cake's defaults: `~/.local/share/cake` for sessions and `~/.cache/cake` for cache state. |
| `-model <name>` | Model for the suggestion call. Empty uses the captured model configuration on reuse and the session's model configuration on resume. A name that differs from the capture skips reuse; a name that differs from `session_meta.model_config` skips resume so the requested model can be used. |
| `-profile <name>` | Profile for the suggestion call. Empty omits the flag and requires the capture to have no profile on reuse. A non-empty profile must match the capture for reuse; otherwise reuse is skipped. |
| `-reasoning-effort <effort>` | Reasoning effort for the suggestion call: `none`, `low`, `medium`, `high`, `xhigh`, or `max`. Empty omits the flag. A non-empty value skips reuse because Cake rejects request-shaping overrides there, then applies to resume or excerpt. |
| `-timeout <duration>` | Whole-request deadline, including replay and up to two model attempts. Default `50s`. cake-repl allows 60s. |
| `-max-transcript-chars <n>` | Bound for the fallback transcript excerpt. Default `20000`; raise it for a session that needs more context. |
| `-max-replay-bytes <n>` | Replay stream bound. Default `67108864` (64 MiB). |
| `-json` | Emit one JSON document. |
| `-verbose` | Print content-free timings, entry counts, token usage (including cached tokens and cache writes), the context mode (`reuse`, `resume`, `excerpt`, or `none`), and a bounded fallback reason to stderr. |

## Security and failure behavior

- Reuse is read-only by implication: `--reuse-last-request` suppresses session
  records, telemetry, hooks, and the writer lock. The read-only resume and the
  excerpt fallback have the same no-write behavior. The fallback also runs
  without a session, tools, skills, project settings, global hooks, MCP
  servers, toolbox tools, or the user's data root.
- Reuse reads only the local request-capture sidecar to preflight it. The
  captured body is verified by length and digest and never logged or sent by
  `cake-suggest`; Cake reads it for the child call. The preflight does not read
  the session JSONL.
- The resume call runs in the session's working directory, so it loads that
  project's trusted Cake configuration and instruction files, as a normal Cake
  run there would. Read-only mode still suppresses hooks; `--tool-choice none`
  asks the provider not to call a tool. Reuse bypasses project context and
  keeps the captured context instead. The reuse instructions carry the output
  schema because the captured body cannot add Cake's usual schema context.
- Reuse preserves the captured tool choice; Cake rejects a returned tool call
  before dispatch, so the tool schemas can stay in the request without any
  execution. The read-only resume passes `--tool-choice none`; it relies on the
  provider honoring that choice. The tool set is kept on both paths only to
  preserve the prompt prefix.
- The transcript is sent only to the model endpoint selected by the user's Cake
  configuration. Reuse and resume send the session's stored conversation; the
  fallback sends the bounded excerpt. Neither is written to a persistent file
  by this tool.
- The fallback's temporary prompt and schema files are created with owner-only
  permissions and removed afterward. Reuse and resume write only a temporary
  schema file.
- `-verbose` and the debug log contract in cake-repl never include transcript
  content, only counts, timings, token usage (including cached tokens), the
  mode, and a bounded fallback reason.
- Replay, model, schema, and output failures are nonzero exits with a bounded
  stderr diagnostic. cake-repl surfaces at most one warning per process.
- A suggestion is untrusted model output. cake-repl bounds it, shows it as
  ghost text, and never parses an accepted suggestion as a slash command.

## Limitations

- Reuse needs a capture from the latest successful persisted task. A missing,
  stale, or incompatible capture, a model/profile mismatch, or a non-empty
  `-reasoning-effort` skips reuse and starts at resume. Cake revalidates the
  exact capture freshness at call time.
- The resume path sends the full stored conversation, so its cost grows with
  the session. The whole-request timeout bounds latency, not provider spend.
  Provider prompt caching is not guaranteed; only the request prefix and
  session identity are preserved. Cached-token counters are provider-reported;
  a zero or absent counter does not prove the provider skipped its cache. The
  reuse path preserves the captured `tool_choice`, so a provider that includes
  it in its cache identity sees the same value as the main turn.
- The fallback is lossy: reasoning is omitted, tool output is clipped, and old
  context is dropped. A suggestion can miss a detail that the full session
  would supply.
- The suggestion call is metered after every completed turn. The fallback's
  transcript bound keeps it small; reuse and resume depend on the session's
  existing context size.
- Model suggestion quality is not evaluated here. The project proves the
  contract, the subprocess behavior, and the document shape; it does not claim
  that suggestions are useful for every session.
- The whole-request timeout covers replay and up to two model attempts. A reuse
  attempt that consumes the deadline leaves no time for resume or excerpt, so
  the run reports a timeout instead of a later-path suggestion.
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

Tests use a fake Cake process for replay, request reuse, the read-only resume
call, and the fallback call. They require no real Cake binary and no model
call.

## Verified

- `just ci` and `go test -race ./...` pass.
- Against a real Cake build and a mock provider, a suggestion ran on the resume
  path in the session's working directory with `tool_choice: none`, kept the
  session id header and the full tool list, returned the JSON document, and
  left the session file and telemetry sidecar unchanged.
- Tests cover reuse arguments and cwd, capture preflight for missing, stale,
  and incompatible captures, the reuse-to-resume-to-excerpt fallback order,
  cached-token reporting, the resume arguments, the excerpt call, and the
  no-op path.
- The earlier excerpt-only verification still holds for the fallback: replay of
  a 487-entry session parsed into a 20,100-character excerpt, and one real
  suggestion call returned a valid one-document result.
- Driven cake-repl in a 100x30 tmux pane against the checked-in fake cake and
  this real binary: the ghost `Add a focused test for the fake path.` appeared
  with the `Ctrl+S use suggestion` hint, and `Ctrl+S` submitted it as a `YOU`
  row.
- Phase 4 real-session validation ran against an isolated temp data and
  config root with the default model. After replacing a 50 KB project
  `AGENTS.md`, request reuse sent 12,849 input tokens with 12,800 cached, while
  read-only resume rebuilt the request with 12,226 input tokens and 0 cached.
  With unchanged context, both paths warmed to 1,920 cached tokens and reuse
  saved the 56-token difference in the appended instructions.
- The tool-choice fix was validated 2026-11-23 with a normal
  `tool_choice: auto` seed: the main turn sent 9,161 input tokens; the first
  reuse preserved `auto` and sent 9,671 with 9,088 cached; the read-only
  resume fallback sent 6,521 with 0 cached. The reuse body kept 6 tools and
  `tool_choice: auto`; the resume body kept the same tools and
  `tool_choice: none`. A second reuse after the `none` resume reported 0
  cached, so provider cache identity still varies; the first post-turn reuse
  is the path that matters.
- Reuse and read-only resume left the session file, telemetry sidecar, request
  capture, and hook log unchanged; only Cake's daily diagnostic log grew.
  Missing, stale, and incompatible captures fell back to resume with typed
  reasons. A missing working directory fell through to excerpt, which ran with
  the temp config and left the hook log unchanged. Normal `--resume` from a
  different cwd failed with the cwd guard and wrote nothing; from the session
  cwd it appended a turn and a capture as expected.

## Related

- [ADR 001](docs/adr/001-suggest-through-replay-and-an-isolated-cake-call.md):
  why replay and the isolated excerpt call exist.
- [ADR 002](docs/adr/002-resume-read-only-for-suggestions.md): why the read-only
  resume and excerpt fallback were chosen. The README is current for the
  reuse-first chain.
- cake-repl ADR 028: the REPL-side trigger, apply, accept, dismiss, and
  provenance contract.
