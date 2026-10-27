# TODO

- Evaluate suggestion quality on a fixed set of real sessions, including one
  compacted session and one session with a long tool output. Record whether the
  suggestion is actionable, one line, and free of restatement.
- Decide whether the suggester should choose a cheaper default model when
  cake-repl does not pass `-model`/`-profile`, or whether cake-repl should grow
  a dedicated suggestion model setting.
- Add a content-free `-print-transcript` or `-dry-run` mode for debugging the
  projection without spending a model call.
- Add a test that runs against a real Cake binary (`CAKE_BIN`) for replay and
  `--output-schema`, kept out of `just ci`.
- Add release automation, a pinned linter, and a vulnerability check once the
  contract is stable.
