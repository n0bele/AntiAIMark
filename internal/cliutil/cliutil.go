// Package cliutil holds the shared plumbing of every CLI facade: argparse
// style interspersed flag parsing, the --lang flag, and the Python-parity
// fatal-error exit paths with i18n rendering.
package cliutil

import (
	"errors"
	"flag"
	"os"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/i18n"
)

// AddLangFlag registers the shared -lang flag on the default FlagSet.
// Pass its value to Init after parsing.
func AddLangFlag(p *string) {
	flag.StringVar(p, "lang", "", "UI language: en|zh|es|fr|ru (default: $ANTIAIMARK_LANG, then system locale)")
}

// Init applies locale resolution (--lang override > ANTIAIMARK_LANG > system
// locale > English). Call once after flag parsing, before any output.
func Init(langOverride string) {
	i18n.SetLocale(i18n.Detect(langOverride))
}

// ParseAllowInterspersed mirrors argparse's tolerance of options after
// positionals ("clean-text in.txt -o out.txt"), which Go's flag package
// rejects natively: re-parse after each positional argument.
func ParseAllowInterspersed() (positional []string) {
	args := os.Args[1:]
	for {
		if err := flag.CommandLine.Parse(args); err != nil {
			os.Exit(2)
		}
		if flag.CommandLine.NArg() == 0 {
			return positional
		}
		positional = append(positional, flag.CommandLine.Arg(0))
		args = flag.CommandLine.Args()[1:]
	}
}

// FatalErr mirrors the Python exit paths: SystemExit(2) errors (binary
// refusal, over-cap input, backup failure) print their localized message and
// exit 2; anything else is an unhandled exception (missing file, write error)
// that Python would surface as a traceback with exit 1.
func FatalErr(err error) {
	var exit2 cleaning.ErrExit2
	var refusal *cleaning.BinaryRefusal
	if errors.As(err, &refusal) || errors.As(err, &exit2) {
		cleaning.Eprint(cleaning.LocalizeError(err, i18n.Locale()))
		os.Exit(2)
	}
	cleaning.Eprint(i18n.T("cli.error", err.Error()))
	os.Exit(1)
}

// EprintLocalized prints an error's localized rendering to stderr (without
// exiting) — used by facades that keep going after a failure.
func EprintLocalized(err error) {
	cleaning.Eprint(cleaning.LocalizeError(err, i18n.Locale()))
}
