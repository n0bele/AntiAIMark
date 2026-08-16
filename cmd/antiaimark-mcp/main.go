// antiaimark-mcp: MCP (Model Context Protocol) server over stdio — the
// standard integration for AI IDEs and agents (Claude Code/Desktop, Cursor,
// Windsurf, Cline, Continue, Zed, ...).
//
// Register it with any MCP client, e.g. for Claude Code:
//
//	claude mcp add antiaimark -- /path/to/antiaimark-mcp
//
// or in a client config:
//
//	{ "mcpServers": { "antiaimark": {
//	    "command": "/path/to/antiaimark-mcp"
//	} } }
//
// Tools: capabilities, inspect_file, clean_file, inspect_text, clean_text.
// Tool descriptions follow the client locale reported at initialize
// (en / zh / es / fr / ru).
package main

import (
	"flag"
	"fmt"
	"os"

	"antiaimark/internal/cliutil"
	"antiaimark/internal/mcp"
)

func main() {
	versionFlag := flag.Bool("V", false, "print version and exit")
	flag.BoolVar(versionFlag, "version", false, "print version and exit")
	var langFlag string
	cliutil.AddLangFlag(&langFlag)
	flag.Parse()
	cliutil.Init(langFlag)

	version := os.Getenv("ANTIAIMARK_SERVER_VERSION")
	if version == "" {
		version = "dev"
	}
	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}

	if err := mcp.New(version).RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}
