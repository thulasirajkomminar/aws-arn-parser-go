// Package main runs the local development server for the AWS ARN parser.
//
// It serves the static UI at "/" and the parse endpoint at "/parse-arn" and
// "/api/parse-arn" (the latter matches the Vercel deployment path).
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

	"github.com/thulasirajkomminar/aws-arn-parser-go/api"
)

const (
	defaultPort       = "8080"
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second

	indexFile = "index.html"
)

func main() {
	err := run()
	if err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func run() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	srv := newServer(port)

	serverErr := make(chan error, 1)
	go listen(srv, port, serverErr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}

	return shutdown(srv)
}

func newServer(port string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/parse-arn", api.Handler)
	mux.HandleFunc("/api/parse-arn", api.Handler)
	mux.HandleFunc("/", serveIndex)

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)

		return
	}

	http.ServeFile(w, r, indexFile)
}

func listen(srv *http.Server, port string, errCh chan<- error) {
	fmt.Printf("Server starting on :%s\n", port)
	fmt.Printf("Try: http://localhost:%s/parse-arn?arn=arn:aws:s3:::my-bucket/folder/file.txt\n", port)

	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- err
	}
}

func shutdown(srv *http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}

	return nil
}
