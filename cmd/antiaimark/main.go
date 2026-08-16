// antiaimark: the merged one-binary facade. Every CLI, the HTTP server and
// the MCP server live behind a single executable; the standalone cmd/*
// binaries (clean-text, antiaimark-server, ...) are retired in favour of:
//
//	antiaimark server
//	antiaimark mcp
//	antiaimark clean-text|inspect-text|clean-image|inspect-image
//	antiaimark clean-file|inspect-file
//	antiaimark rewrite-text|audit-dir|audit-website|healthcheck
//
// All implementations are in internal/cliapp and share one core library.
package main

import (
	"fmt"
	"io"
	"os"

	"antiaimark/internal/cliapp"
)

// buildVersion is stamped at release time via -ldflags
// "-X antiaimark/cmd/antiaimark.buildVersion=<tag>"; ANTIAIMARK_SERVER_VERSION
// still takes precedence at runtime for the server/mcp subcommands.
var buildVersion = "dev"

type command struct {
	name, desc string
}

var commands = []command{
	{"server", "run the HTTP service + web UI (127.0.0.1:8765)"},
	{"mcp", "run the MCP server over stdio for AI IDEs"},
	{"inspect-text", "detect invisible Unicode/AI traces in text"},
	{"clean-text", "remove AI traces from text"},
	{"inspect-image", "detect C2PA/AI metadata in PNG/JPEG/WebP"},
	{"clean-image", "strip C2PA/AI metadata from images (pixels untouched)"},
	{"inspect-file", "unified inspect: auto-detect text/image/container"},
	{"clean-file", "unified clean: auto-detect text/image/container"},
	{"rewrite-text", "Layer B rewrite against statistical watermarks"},
	{"audit-dir", "aggregate AI-provenance audit over a directory"},
	{"audit-website", "aggregate audit over a sitemap"},
	{"healthcheck", "probe the HTTP service /health endpoint"},
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "help", "-h", "--help", "-help":
		usage(os.Stdout)
		os.Exit(0)
	case "server":
		os.Exit(cliapp.Server(args[1:], buildVersion))
	case "mcp":
		os.Exit(cliapp.MCP(args[1:], buildVersion))
	case "healthcheck":
		os.Exit(cliapp.Healthcheck(args[1:]))
	case "clean-text":
		os.Exit(cliapp.CleanText(args[1:]))
	case "inspect-text":
		os.Exit(cliapp.InspectText(args[1:]))
	case "clean-image":
		os.Exit(cliapp.CleanImage(args[1:]))
	case "inspect-image":
		os.Exit(cliapp.InspectImage(args[1:]))
	case "clean-file":
		os.Exit(cliapp.CleanFile(args[1:]))
	case "inspect-file":
		os.Exit(cliapp.InspectFile(args[1:]))
	case "rewrite-text":
		os.Exit(cliapp.RewriteText(args[1:]))
	case "audit-dir":
		os.Exit(cliapp.AuditDir(args[1:]))
	case "audit-website":
		os.Exit(cliapp.AuditWebsite(args[1:]))
	default:
		fmt.Fprintf(os.Stderr, "antiaimark: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "antiaimark - detect and remove AI provenance marks (one binary, many subcommands)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: antiaimark <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-16s %s\n", c.name, c.desc)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'antiaimark <command> -h' for command flags.")
}
