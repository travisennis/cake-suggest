// Command cake-suggest proposes the next user prompt for an existing Cake
// session.
//
// It reads the session metadata through Cake's read-only replay stream and
// first asks Cake to reuse the exact request captured after the completed
// turn. If that cannot run, it resumes the session read-only. If neither can
// run, it falls back to one isolated, tool-less Cake model call over a bounded
// transcript excerpt. It never writes to the session and never reads Cake
// session files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/travisennis/cake-suggest/internal/cakecli"
	"github.com/travisennis/cake-suggest/internal/suggest"
	"github.com/travisennis/cake-suggest/internal/version"
)

const (
	defaultTimeout     = 50 * time.Second
	defaultReplayBytes = 64 << 20
)

func main() {
	os.Exit(runCLI(os.Args[1:]))
}

func runCLI(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "suggest":
		return commandSuggest(args[1:])
	case "help", "-h", "--help":
		usage()
		return 0
	case "version", "-version", "-V", "--version":
		fmt.Println(version.Text())
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cake-suggest - propose the next user prompt for a Cake session

Usage:
  cake-suggest suggest [flags] -- <session-id>
  cake-suggest version | -version | -V | --version
  cake-suggest help | -h | --help

The session is read through Cake's read-only replay stream. The suggestion
call first reuses the exact provider request captured after the completed
turn, then falls back to a read-only resume with tool choice none, then to a
bounded replay excerpt. Reuse and resume never append to the session. Nothing
is written to the session.

Flags:
  -cake-bin <path>             cake executable (default: $CAKE_BIN or cake)
  -data-dir <path>             cake data root (default: $CAKE_DATA_DIR or ~/.local/share/cake)
  -model <name>                model for the suggestion call
  -profile <name>              profile for the suggestion call
  -reasoning-effort <effort>   reasoning effort (none, low, medium, high, xhigh, max)
  -timeout <duration>          whole-request deadline (default 50s)
  -max-transcript-chars <n>    fallback transcript excerpt bound (default 20000)
  -max-replay-bytes <n>        replay stream bound (default 67108864)
  -json                        emit one JSON document
  -verbose                     print content-free timings and usage (including cached tokens) on stderr

JSON result:
  {"suggested":true,"suggestion":"...","session_id":"..."}
  {"suggested":false,"reason":"...","session_id":"..."}

Exit codes:
  0  a suggestion or a deliberate no-op
  1  replay, model, or output failure
  2  invalid usage

Version output:
  cake-suggest 0.1.0
  revision <commit>
  time <commit-time>

Examples:
  cake-suggest suggest -json -- 01234567-89ab-4cde-8f01-23456789abcd
  cake-suggest suggest -model fast -profile concise -reasoning-effort low -json -- <session-id>
`)
}

func commandSuggest(args []string) int {
	fs := flag.NewFlagSet("suggest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		cakeBin         string
		dataDir         string
		model           string
		profile         string
		reasoningEffort string
		timeout         time.Duration
		maxTranscript   int
		maxReplayBytes  int64
		jsonOut         bool
		verbose         bool
	)
	fs.StringVar(&cakeBin, "cake-bin", "", "cake executable")
	fs.StringVar(&dataDir, "data-dir", "", "cake data root")
	fs.StringVar(&model, "model", "", "model for the suggestion call")
	fs.StringVar(&profile, "profile", "", "profile for the suggestion call")
	fs.StringVar(&reasoningEffort, "reasoning-effort", "", "reasoning effort for the suggestion call")
	fs.DurationVar(&timeout, "timeout", defaultTimeout, "whole-request deadline")
	fs.IntVar(&maxTranscript, "max-transcript-chars", 20_000, "fallback transcript excerpt bound")
	fs.Int64Var(&maxReplayBytes, "max-replay-bytes", defaultReplayBytes, "replay stream bound")
	fs.BoolVar(&jsonOut, "json", false, "emit one JSON document")
	fs.BoolVar(&verbose, "verbose", false, "print timings and usage on stderr")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "suggest requires exactly one session id after --")
		return 2
	}
	sessionID := fs.Arg(0)
	if !cakecli.ValidSessionID(sessionID) {
		fmt.Fprintf(os.Stderr, "invalid session id %q: expected a lowercase UUID\n", sessionID)
		return 2
	}
	if timeout <= 0 {
		fmt.Fprintln(os.Stderr, "-timeout must be positive")
		return 2
	}
	if maxTranscript <= 0 {
		fmt.Fprintln(os.Stderr, "-max-transcript-chars must be positive")
		return 2
	}
	if maxReplayBytes <= 0 {
		fmt.Fprintln(os.Stderr, "-max-replay-bytes must be positive")
		return 2
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()

	result, err := suggest.Run(ctx, suggest.Options{
		CakeBin:            cakeBin,
		DataDir:            dataDir,
		SessionID:          sessionID,
		Model:              model,
		Profile:            profile,
		ReasoningEffort:    reasoningEffort,
		MaxTranscriptChars: maxTranscript,
		MaxReplayBytes:     maxReplayBytes,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cake-suggest:", err)
		return 1
	}

	if verbose {
		fmt.Fprintf(os.Stderr,
			"cake-suggest: mode=%s entries=%d chars=%d omitted=%d replay_ms=%d model_ms=%d tokens_in=%d tokens_out=%d cached_tokens=%d cache_write_tokens=%d\n",
			result.Mode,
			result.TranscriptEntries,
			result.TranscriptChars,
			result.OmittedEntries,
			result.ReplayElapsed.Milliseconds(),
			result.ModelElapsed.Milliseconds(),
			result.Usage.InputTokens,
			result.Usage.OutputTokens,
			result.Usage.InputTokensDetails.CachedTokens,
			result.Usage.InputTokensDetails.CacheWriteTokens,
		)
		if result.FallbackReason != "" {
			fmt.Fprintf(os.Stderr, "cake-suggest: fallback: %s\n", result.FallbackReason)
		}
	}

	if jsonOut {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result.Document); err != nil {
			fmt.Fprintln(os.Stderr, "cake-suggest: write result:", err)
			return 1
		}
		return 0
	}

	if result.Suggested {
		fmt.Println(result.Suggestion)
		return 0
	}
	if result.Reason != "" {
		fmt.Fprintln(os.Stderr, "cake-suggest:", result.Reason)
	}
	return 0
}
