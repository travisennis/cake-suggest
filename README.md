# cake-suggest

An external prompt suggester for [Cake](https://github.com/travisennis/cake)
sessions. It reads the session metadata through Cake's read-only replay stream
and asks Cake to reuse the exact provider request captured after the completed
turn. It returns one JSON document, never writes to the session, and never
reads Cake session files.

This is the suggester side of the contract defined by cake-repl ADR 028
("Offer after-turn prompt suggestions from an external suggester"). cake-repl
runs it after a completed turn and shows a usable suggestion as ghost text in
the composer.

Reuse is the only suggestion path. A capture that cannot be reused produces a
no-op with a reason; it does not fall back to a rebuilt resume or a transcript
excerpt, because those send a different request than the turn ended with.

## Requirements

- A Cake build that supports:
  - `cake --output-format stream-json replay <uuid>`
  - `cake --resume <uuid> --reuse-last-request --output-format json`
  - `--output-schema` on a normal Cake run
- Go 1.25 or newer to build.
- A model/profile configured for Cake. Reuse uses the captured request's model
  configuration when `-model` is empty.

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
{"suggested":false,"reason":"capture_unavailable: no request capture for session ...","session_id":"01234567-89ab-4cde-8f01-23456789abcd"}
```

`suggested:false` is a deliberate no-op, not a failure. It covers both the
model deciding there is no next step and the local preflight deciding the
capture cannot be reused. The `reason` field names which. Unknown fields are
ignored by cake-repl. A failure exits nonzero, writes a diagnostic to stderr,
and writes nothing to stdout. The suggestion is normalized to one line and at
most 300 characters before it is returned.

Without `-json`, the suggestion is printed to stdout; a no-op reason goes to
stderr.

## How it works

1. **Replay the session read-only.** Run
   `cake --output-format stream-json replay <uuid>`. This opens the session
   read-only: no lock, no append, no network. It supplies the session id, the
   working directory, and the latest task id.
2. **Preflight the captured request.** Read
   `<cache>/session-requests/<uuid>.json` only. Verify the capture version,
   session id, body length and digest, backend, provider model configuration,
   profile, latest task id, and that the body has the message array Cake
   appends to (`messages` for Chat Completions, `input` for Responses). The
   captured body stays local: it is verified by length and digest and is never
   logged or sent by `cake-suggest`.
3. **Reuse the captured request.** Run:

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

   Cake revalidates the exact session file length, current provider identity,
   and profile before sending. A capture that passed the local preflight but
   fails that revalidation is a nonzero failure, not a no-op.
4. **Validate the one document.** The model must return
   `{"suggested":boolean,"suggestion":string,"reason":string}`. A missing
   `suggested`, an empty suggestion when `suggested` is true, or malformed JSON
   is a failure. The suggestion is reported with the requested session id.

### No-op reasons

The run is a no-op without a model call when:

- the capture is missing (`capture_unavailable`);
- the capture is from an older task than the session's latest task
  (`capture_stale`);
- the capture version, backend, body integrity, model configuration, or
  profile is unusable (`capture_incompatible`);
- `-model` names a different `[[models]]` entry than the capture;
- `-profile` differs from the capture;
- `-reasoning-effort` is non-empty, because a reused request cannot be
  reshaped.

### Why this shape

- **Replay, not session files.** The replay stream is a documented, read-only
  contract. Reading `~/.local/share/cake/sessions/*.jsonl` would couple this
  tool to Cake's record format and duplicate its projector.
- **Exact request reuse, not semantic reconstruction.** A normal resume
  rebuilds the system prompt, project context, skills, MCP catalog, and tools
  from the current invocation. The captured body already contains the request
  the completed turn ended with, so reusing it preserves the provider prefix
  and gives prompt caching its best chance without changing the session.
- **One path, not a fallback chain.** A read-only resume or a bounded excerpt
  answers a different question with a request the provider has never seen. Both
  cost a full request and produce a worse suggestion, and a silent fallback
  hides the fact that the capture is missing or stale. A no-op with a typed
  reason is cheaper, clearer, and leaves one request shape to maintain.
- **Preserve the captured tool choice.** Removing tools changes the request's
  tool schemas and prompt prefix. Reuse keeps the captured choice and rejects
  any returned tool call before dispatch.

## Usage

```sh
# The shape cake-repl uses.
cake-suggest suggest -json -- 01234567-89ab-4cde-8f01-23456789abcd

# Human-readable output.
cake-suggest suggest -- 01234567-89ab-4cde-8f01-23456789abcd

# Show the no-op reason and the token usage.
cake-suggest suggest -json -verbose -- <session-id>

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

Leave `[suggestions] model`, `profile`, and `reasoning-effort` unset unless
they match the session. cake-repl passes its effective model and profile by
default; a value that does not match the capture makes the run a no-op.

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
| `-data-dir <path>` | Cake data root for replay and reuse. When set, Cake also uses it as the cache root for the request-capture sidecar. Default `$CAKE_DATA_DIR`, then Cake's defaults: `~/.local/share/cake` for sessions and `~/.cache/cake` for cache state. |
| `-model <name>` | Model entry for the suggestion. Empty uses the capture's model configuration. A name that differs from the capture makes the run a no-op. |
| `-profile <name>` | Profile for the suggestion. Empty requires the capture to have no profile. A non-empty profile must match the capture. |
| `-reasoning-effort <effort>` | Accepted for caller compatibility. A non-empty value makes the run a no-op because Cake rejects request-shaping overrides on reuse. |
| `-timeout <duration>` | Whole-request deadline, including replay and the reuse call. Default `50s`. cake-repl allows 60s. |
| `-max-replay-bytes <n>` | Replay stream bound. Default `67108864` (64 MiB). |
| `-json` | Emit one JSON document. |
| `-verbose` | Print content-free timings, the mode (`reuse` or `none`), token usage (including cached tokens and cache writes), and a no-op reason to stderr. |

## Security and failure behavior

- Reuse is read-only by implication: `--reuse-last-request` suppresses session
  records, telemetry, hooks, and the writer lock.
- Reuse reads only the local request-capture sidecar to preflight it. The
  captured body is verified by length and digest and never logged or sent by
  `cake-suggest`; Cake reads it for the child call. The preflight does not read
  the session JSONL.
- The reuse call runs in the session's working directory when it still exists.
  Cake resolves the captured model configuration from the directory stored in
  the session; the child cwd is only the fallback when the header has none.
- The captured conversation is sent only to the model endpoint selected by
  the user's Cake configuration. `cake-suggest` never logs it and never writes
  it to a persistent file; Cake reads the capture for the child call.
- The temporary schema file is created with owner-only permissions and removed
  afterward.
- `-verbose` and the debug log contract in cake-repl never include conversation
  content, only counts, timings, token usage (including cached tokens), the
  mode, and a bounded no-op reason.
- Replay, model, schema, and output failures are nonzero exits with a bounded
  stderr diagnostic. cake-repl surfaces at most one warning per process.
- A suggestion is untrusted model output. cake-repl bounds it, shows it as
  ghost text, and never parses an accepted suggestion as a slash command.

## Limitations

- Reuse needs a capture from the latest successful persisted task. Sessions
  created before the capture feature, sessions whose last turn failed, and
  sessions whose capture was deleted produce no suggestion. Continue the
  session once to write a capture.
- Settings drift is caught by Cake, not the local preflight. A changed provider
  identity, model entry, or profile makes the reuse call fail (exit 1). The
  local preflight cannot see current settings without reading Cake's config.
- A non-empty `-reasoning-effort` disables suggestions. The captured request
  already carries the reasoning settings the main turn used.
- The run sends the captured request, so its cost follows the session's context
  size. The whole-request timeout bounds latency, not provider spend. Provider
  prompt caching is not guaranteed; only the request prefix and session
  identity are preserved. Cached-token counters are provider-reported; a zero
  or absent counter does not prove the provider skipped its cache.
- The suggestion call is metered after every completed turn. It is skipped
  without a model call when the capture is unusable.
- Model suggestion quality is not evaluated here. The project proves the
  contract, the subprocess behavior, and the document shape; it does not claim
  that suggestions are useful for every session.
- The suggestion is not editable in place. cake-repl accepts it with `Ctrl+S`
  or discards it when the user types; editing one word means accepting it
  first or typing a new prompt.
- `--output-schema` is required. An older Cake build without request reuse
  fails the suggestion call rather than silently using a different path.

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

Tests use a fake Cake process for replay and request reuse. They require no real
Cake binary and no model call.

## Verified

- `just ci` and `go test -race ./...` pass.
- Against a real Cake build and a mock provider, request reuse preserved the
  captured system prompt, tools, and `tool_choice`, returned the JSON document,
  and left the session file, telemetry sidecar, request capture, and hook log
  unchanged; only Cake's daily diagnostic log grew.
- Phase 4 real-session validation: after replacing a 50 KB project `AGENTS.md`,
  request reuse sent 12,849 input tokens with 12,800 cached, while a read-only
  resume rebuilt the request with 12,226 input tokens and 0 cached. With
  unchanged context, both paths warmed to 1,920 cached tokens and reuse saved
  the 56-token difference in the appended instructions.
- The tool-choice fix was validated 2026-11-23 with a normal `tool_choice: auto`
  seed: the first reuse preserved `auto` and sent 9,671 input tokens with 9,088
  cached; rewriting the same capture to `none` sent 6,521 with 0 cached.
- Capture preflight failures (missing, stale, incompatible, model mismatch,
  non-empty reasoning effort) produce a no-op and do not invoke Cake's model
  call. The 2026-11-24 reuse-only change is covered by `just ci`, `just
  test-race`, and CLI tests that assert the no-op document, the reuse arguments,
  and the absence of resume/excerpt flags.

## Related

- [ADR 001](docs/adr/001-suggest-through-replay-and-an-isolated-cake-call.md):
  why replay and an isolated call exist. Historical; the isolated excerpt path
  was removed 2026-11-24.
- [ADR 002](docs/adr/002-resume-read-only-for-suggestions.md): why the
  read-only resume and excerpt fallback were chosen. Historical; the fallback
  chain was removed 2026-11-24.
- cake-repl ADR 028: the REPL-side trigger, apply, accept, dismiss, and
  provenance contract.
