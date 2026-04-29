package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// ReadTimeout/WriteTimeout intentionally unset: cc uploads can be large
		// and SSE streams are long-lived, so a global timeout would cut them off.
	}

	log.Printf("claudeproxy listening on http://%s upstream=%s fallback_configured=%v",
		addr, upstream, fallbackKey != "")

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	log.Printf("received %s, shutting down", s)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
