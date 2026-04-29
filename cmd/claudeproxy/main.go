package main

import (
	"log"
	"net/http"
	"os"

	"github.com/unsafe9/claude-code-proxy/internal/proxy"
)

func main() {
	port := getenv("PROXY_PORT", "8787")
	upstream := getenv("UPSTREAM_URL", "https://api.anthropic.com")
	fallbackKey := os.Getenv("ANTHROPIC_FALLBACK_API_KEY")

	state := proxy.NewState()
	logger := proxy.NewLogger(os.Stderr)
	handler := proxy.NewHandler(upstream, fallbackKey, state, logger)

	addr := "127.0.0.1:" + port
	log.Printf("claudeproxy listening on http://%s upstream=%s fallback_configured=%v",
		addr, upstream, fallbackKey != "")
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
