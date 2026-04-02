// fog-of-war-clearer HTTP API server – expose the analysis tool as a REST API.
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

	"github.com/ken-guru/fog-of-war-clearer/internal/api"
	"github.com/ken-guru/fog-of-war-clearer/internal/checker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	addr := listenAddr()

	c, err := checker.New()
	if err != nil {
		return fmt.Errorf("initialise checker: %w", err)
	}

	mux := http.NewServeMux()
	api.NewHandler(c).RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Minute, // long analyses may take a while
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown on SIGTERM / SIGINT.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("fog-of-war-clearer API listening on %s", addr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-stop:
		log.Println("shutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

// listenAddr returns the address to listen on from the PORT environment variable,
// defaulting to ":8080".
func listenAddr() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return ":" + port
}
