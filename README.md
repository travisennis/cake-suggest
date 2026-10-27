# cake-suggest

An external prompt suggester for [Cake](https://github.com/travisennis/cake)
sessions. It reads an existing session through Cake's read-only replay stream,
asks one isolated Cake model call for the next user prompt, and returns one
JSON document. It never writes to the session and never reads Cake session
files.

This is the suggester side of the contract defined by cake-repl ADR 028
("Offer after-turn prompt suggestions from an external suggester"). cake-repl
runs it after a completed turn and shows a usable suggestion as ghost text in
the composer.

## Requirements

- A Cake build that supports:
  - `cake --output-format stream-json replay <uuid>`
  - `--output-schema` on a normal Cake run
- Go 1.25 or newer to build.
- A model/profile configured for Cake. The suggestion call uses the `-model`
  and `-profile` it is given, or Cake's defaults.

`cake-suggest` checks neither capability up front. A replay that Cake rejects
or a model call that Cake rejects is reported on stderr with a nonzero exit.

## Contract

cake-repl invokes exactly:

```sh
cake-suggest suggest -json [-cake-bin <path>] [-model <name>] [-profile <name>] -- <session-id>
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
most 500 characters before it is returned.

Without `-json`, the suggestion is printed to stdout; a no-op reason goes to
stderr.

## How it works

1. **Replay the session read-only.** Run
   `cake --output-format stream-json replay <uuid>`. This opens the session
   read-only: no lock, no append, no network. It is the documented transcript
   projection, not Cake's private JSONL format.
2. **Build a bounded transcript.** Keep `USER` and `ASSISTANT` messages,
   labeled tool calls and results, and a checkpoint's `HISTORICAL SUMMARY`.
   Ignore reasoning, usage, lifecycle, and hook records. Each block is clipped;
   the newest blocks are kept within `-max-transcript-chars` (default 20,000).
   Older blocks are replaced by one omission marker.
3. **Ask one isolated model call.** Run Cake in a temporary directory with a
   temporary data root:

   ```sh
   cake --no-session --no-tools --no-skills --output-format json \
     --system-prompt <temp> --output-schema <temp> [-model ...] [-profile ...] -
   ```

   The transcript is on stdin. The temporary data root means the call cannot
   pollute the user's sessions or telemetry; the temporary working directory
   means project settings, hooks, skills, and `AGENTS.md` do not apply. No
   session is created, resumed, or forked.
4. **Validate the one document.** The model must return
   `{"suggested":boolean,"suggestion":string,"reason":string}`. A missing
   `suggested`, an empty suggestion when `suggested` is true, or malformed JSON
   is a failure. The suggestion is reported with the requested session id.

### Why this shape

- **Replay, not session files.** The replay stream is a documented, read-only
  contract. Reading `~/.local/share/cake/sessions/*.jsonl` would couple this
  tool to Cake's record format and duplicate its projector.
- **No `--resume` or `--fork`.** A resumed call would append a turn to the real
  session. A fork would create a new session and copy history just to ask a
  question. Both make a read-only suggestion mutating or expensive.
- **No tools.** The suggester only needs to read the transcript and write a
  prompt. Removing tools and skills keeps the call cheap and removes a large
  prompt-injection payoff.
- **A bounded, lossy transcript.** The full raw session can be arbitrarily
  large. A suggestion needs the current state, so the newest conversation
  excerpt is enough. The bound and the omission marker are explicit.

## Usage

```sh
# The shape cake-repl uses.
cake-suggest suggest -json -- 01234567-89ab-4cde-8f01-23456789abcd

# Human-readable output.
cake-suggest suggest -- 01234567-89ab-4cde-8f01-23456789abcd

# Use a specific model/profile and show content-free timing/cost diagnostics.
cake-suggest suggest -json -verbose \
  -model fast -profile concise -- <session-id>

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

## Flags

| Flag | Meaning |
| --- | --- |
| `-cake-bin <path>` | Cake executable. Default `$CAKE_BIN`, then `cake` on `PATH`. |
| `-data-dir <path>` | Cake data root for replay. Default `$CAKE_DATA_DIR`, then `~/.local/share/cake`. |
| `-model <name>` | Model for the suggestion call. Empty uses Cake's default. |
| `-profile <name>` | Profile for the suggestion call. Empty omits the flag. |
| `-timeout <duration>` | Whole-request deadline, including replay and the model call. Default `50s`. cake-repl allows 60s. |
| `-max-transcript-chars <n>` | Transcript excerpt bound. Default `20000`; raise it for a session that needs more context. |
| `-max-replay-bytes <n>` | Replay stream bound. Default `67108864` (64 MiB). |
| `-json` | Emit one JSON document. |
| `-verbose` | Print content-free timings, entry counts, and token usage to stderr. |

## Security and failure behavior

- The transcript is sent only to the model endpoint selected by the user's Cake
  configuration. It is not written to a persistent file by this tool.
- The model call runs without tools or skills, in a temporary working
  directory and a temporary Cake data root. The temporary prompt and schema
  files are created with owner-only permissions and removed afterward.
- `-verbose` and the debug log contract in cake-repl never include transcript
  content, only counts, timings, and token usage.
- Replay, model, schema, and output failures are nonzero exits with a bounded
  stderr diagnostic. cake-repl surfaces at most one warning per process.
- A suggestion is untrusted model output. cake-repl bounds it, shows it as
  ghost text, and never parses an accepted suggestion as a slash command.

## Limitations

- The transcript is a projection: reasoning is omitted, tool output is clipped,
  and old context is dropped. A suggestion can miss a detail that the full
  session would supply.
- The suggestion call is metered after every completed turn. The default
  transcript bound keeps it small, but cost still depends on the selected
  model and profile.
- Model suggestion quality is not evaluated here. The project proves the
  contract, the bounded subprocess behavior, and the document shape; it does
  not claim that suggestions are useful for every session.
- The suggestion is not editable in place. cake-repl accepts it with `Ctrl+S`
  or discards it when the user types; editing one word means accepting it
  first or typing a new prompt.
- `--output-schema` is required. An older Cake build without it fails the
  suggestion call rather than falling back to unvalidated text.

## Build and test

```sh
just build          # bin/cake-suggest
just install        # go install ./cmd/cake-suggest
just test
just test-race
just ci             # fmt-check, vet, test, build
```

Tests use a fake Cake process for both replay and the model call. They require
no real Cake binary and no model call.

## Verified

- `just ci` and `go test -race ./...` pass.
- Against a real Cake build, replay of a 487-entry session parsed into a
  20,100-character excerpt, and one real suggestion call returned a valid
  one-document result (6,686 input / 245 output tokens, about 4.1 seconds).
- Driven cake-repl in a 100x30 tmux pane against the checked-in fake cake and
  this real binary: the ghost `Add a focused test for the fake path.` appeared
  with the `Ctrl+S use suggestion` hint, and `Ctrl+S` submitted it as a `YOU`
  row.

## Related

- [ADR 001](docs/adr/001-suggest-through-replay-and-an-isolated-cake-call.md):
  why this uses replay and an isolated no-session call.
- cake-repl ADR 028: the REPL-side trigger, apply, accept, dismiss, and
  provenance contract.
