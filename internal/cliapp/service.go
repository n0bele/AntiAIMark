package cliapp

// Service commands: HTTP facade (server), MCP stdio server (mcp) and the
// dependency-free health probe (healthcheck). The merged antiaimark binary
// forwards its -V/version handling through the version parameter, which the
// standalone wrappers stamp via -ldflags.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/httpapi"
	"antiaimark/internal/i18n"
	"antiaimark/internal/janitor"
	"antiaimark/internal/mcp"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envDurationOr(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envFloatOr(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 100 {
			return f
		}
	}
	return def
}

// Server runs the HTTP facade: /health, /capabilities, /openapi.json,
// /inspect, /clean, the /mcp Streamable HTTP endpoint, the embedded web UI,
// /api/upload and /api/download. Blocks until the server exits.
func Server(args []string, version string) int {
	fs := newFlagSet("antiaimark-server")
	hostFlag := fs.String("host", envOr("ANTIAIMARK_SERVER_HOST", "127.0.0.1"), "bind address")
	portFlag := fs.String("port", envOr("ANTIAIMARK_SERVER_PORT", "8765"), "bind port")
	apiKeyFlag := fs.String("api-key", strings.TrimSpace(os.Getenv("ANTIAIMARK_SERVER_API_KEY")), "require this bearer token (default: none)")
	versionFlag := fs.Bool("V", false, "print version and exit")
	fs.BoolVar(versionFlag, "version", false, "print version and exit")
	autoClean := fs.Bool("auto-clean", envBool("ANTIAIMARK_AUTO_CLEAN"), "enable background auto-clean: evict expired downloads, and free space by deleting this service's stale temp dirs when free disk drops below the threshold")
	autoCleanInterval := fs.Duration("auto-clean-interval", envDurationOr("ANTIAIMARK_AUTO_CLEAN_INTERVAL", 15*time.Minute), "auto-clean check period")
	autoCleanThreshold := fs.Float64("auto-clean-threshold", envFloatOr("ANTIAIMARK_AUTO_CLEAN_THRESHOLD", 11), "free-space percentage below which auto-clean triggers (0-100)")
	autoCleanTTL := fs.Duration("auto-clean-ttl", envDurationOr("ANTIAIMARK_AUTO_CLEAN_TTL", 24*time.Hour), "download retention before eviction")
	var langFlag string
	cliutil.AddLangFlagFS(fs, &langFlag)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cliutil.Init(langFlag)

	if v := os.Getenv("ANTIAIMARK_SERVER_VERSION"); v != "" {
		version = v
	}

	if *versionFlag {
		fmt.Println(version)
		return 0
	}

	host, port := *hostFlag, *portFlag

	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		cleaning.Eprint(i18n.T("server.warn_bind", host))
	}
	if *apiKeyFlag != "" {
		cleaning.Eprint(i18n.T("server.key_required"))
	} else {
		cleaning.Eprint(i18n.T("server.warn_key_unset"))
	}

	api := httpapi.New(httpapi.Options{Version: version, APIKey: strings.TrimSpace(*apiKeyFlag)})

	if !*autoClean {
		cleaning.Eprint(i18n.T("janitor.disabled"))
	} else {
		stop := janitor.Start(context.Background(), janitor.Config{
			Enabled:               true,
			Interval:              *autoCleanInterval,
			Threshold:             *autoCleanThreshold,
			DownloadTTL:           *autoCleanTTL,
			EvictExpiredDownloads: api.EvictExpiredDownloads,
			PurgeDownloads:        api.PurgeDownloads,
			Log:                   func(msg string) { cleaning.Eprint(msg) },
		})
		defer stop()
	}

	addr := host + ":" + port
	cleaning.Eprint(i18n.T("server.startup", version, addr))
	server := &http.Server{Addr: addr, Handler: api.Handler()}
	if err := server.ListenAndServe(); err != nil {
		cleaning.Eprint(i18n.T("server.shutting_down") + ": " + err.Error())
	}
	return 0
}

// MCP runs the Model Context Protocol server over stdio — the standard
// integration for AI IDEs and agents (Claude Code/Desktop, Cursor, Windsurf,
// Cline, Continue, Zed, ...). Register the merged binary as e.g.
//
//	claude mcp add antiaimark -- /path/to/antiaimark mcp
func MCP(args []string, version string) int {
	fs := newFlagSet("antiaimark-mcp")
	versionFlag := fs.Bool("V", false, "print version and exit")
	fs.BoolVar(versionFlag, "version", false, "print version and exit")
	var langFlag string
	cliutil.AddLangFlagFS(fs, &langFlag)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cliutil.Init(langFlag)

	if v := os.Getenv("ANTIAIMARK_SERVER_VERSION"); v != "" {
		version = v
	}
	if *versionFlag {
		fmt.Println(version)
		return 0
	}

	if err := mcp.New(version).RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		return 1
	}
	return 0
}

// Healthcheck probes the antiaimark service /health endpoint and returns 0 on
// HTTP 200. Used by Docker HEALTHCHECK and systemd watchdogs; has no external
// dependencies so it runs on distroless images.
func Healthcheck(args []string) int {
	_ = args
	port := os.Getenv("ANTIAIMARK_SERVER_PORT")
	if port == "" {
		port = "8765"
	}
	url := "http://127.0.0.1:" + port + "/health"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: "+err.Error())
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
