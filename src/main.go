// Command bookings-api is the Bookings API backend: net/http, standard library
// only, state in one JSON file. See ../docker-compose.yml to run it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// `bookings-api healthcheck` is the container healthcheck: the final image
	// has no shell or curl, so the binary probes itself.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(LoadConfig(os.Getenv).Port))
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	cfg := LoadConfig(os.Getenv)
	if len(cfg.APIKeys) == 0 && len(cfg.ReadOnlyKeys) == 0 {
		logger.Println("warning: API_KEYS and API_KEYS_READONLY are empty; every /v1 request will be 401")
	}
	store, err := OpenStore(cfg.DataFile)
	if err != nil {
		logger.Fatalf("opening state: %v", err)
	}
	srv := NewServer(store, cfg, logger)
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Printf("bookings-api listening on :%s (state: %s, test routes: %t)", cfg.Port, cfg.DataFile, cfg.EnableTestRoutes)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("server: %v", err)
	}
	logger.Println("stopped")
}

func healthcheck(port string) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
