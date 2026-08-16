// antiaimark-server: thin entrypoint for the embeddable HTTP facade.
//
// Endpoints (Python server.py compatible):
//
//	GET  /health /capabilities /openapi.json
//	POST /inspect /clean        (JSON, base64 file bodies)
//
// Go web extension:
//
//	GET  /                    embedded web UI (drag & drop, 5 languages)
//	POST /api/upload          multipart file upload -> inspect + clean
//	GET  /api/download/{token} one-shot cleaned-file download
//	GET  /api/i18n?lang=zh    web-UI message catalog
//
// Background auto-clean (opt-in):
//
//	--auto-clean              enable (or ANTIAIMARK_AUTO_CLEAN=1)
//	--auto-clean-interval     check period (default 15m; env ANTIAIMARK_AUTO_CLEAN_INTERVAL)
//	--auto-clean-threshold    free-space %% that triggers cleanup (default 11; env ANTIAIMARK_AUTO_CLEAN_THRESHOLD)
//	--auto-clean-ttl          download retention before eviction (default 24h; env ANTIAIMARK_AUTO_CLEAN_TTL)
//
// Embed the same API in your own program with internal/httpapi.New(...).
package main

import (
	"context"
	"flag"
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

func main() {
	hostFlag := flag.String("host", envOr("ANTIAIMARK_SERVER_HOST", "127.0.0.1"), "bind address")
	portFlag := flag.String("port", envOr("ANTIAIMARK_SERVER_PORT", "8765"), "bind port")
	apiKeyFlag := flag.String("api-key", strings.TrimSpace(os.Getenv("ANTIAIMARK_SERVER_API_KEY")), "require this bearer token (default: none)")
	versionFlag := flag.Bool("V", false, "print version and exit")
	flag.BoolVar(versionFlag, "version", false, "print version and exit")
	autoClean := flag.Bool("auto-clean", envBool("ANTIAIMARK_AUTO_CLEAN"), "enable background auto-clean: evict expired downloads, and free space by deleting this service's stale temp dirs when free disk drops below the threshold")
	autoCleanInterval := flag.Duration("auto-clean-interval", envDurationOr("ANTIAIMARK_AUTO_CLEAN_INTERVAL", 15*time.Minute), "auto-clean check period")
	autoCleanThreshold := flag.Float64("auto-clean-threshold", envFloatOr("ANTIAIMARK_AUTO_CLEAN_THRESHOLD", 11), "free-space percentage below which auto-clean triggers (0-100)")
	autoCleanTTL := flag.Duration("auto-clean-ttl", envDurationOr("ANTIAIMARK_AUTO_CLEAN_TTL", 24*time.Hour), "download retention before eviction")
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
}
