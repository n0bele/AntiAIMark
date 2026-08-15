// healthcheck: probes the watermarks-remover service /health endpoint and
// exits 0 on HTTP 200. Used by Docker HEALTHCHECK and systemd watchdogs;
// has no external dependencies so it runs on distroless images.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("WATERMARKS_SERVER_PORT")
	if port == "" {
		port = "8765"
	}
	url := "http://127.0.0.1:" + port + "/health"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: "+err.Error())
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}
