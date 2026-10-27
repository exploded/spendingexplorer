// Command spendingexplorer serves the explorer from a read-only SQLite database.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/handlers"
	"spendingexplorer/web"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "spending.db"
	}

	conn, err := db.OpenReadOnly(dbPath)
	if err != nil {
		slog.Error("open database", "path", dbPath, "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	h, err := handlers.New(db.New(conn), web.Templates)
	if err != nil {
		slog.Error("load templates", "error", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static)))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		slog.Info("listening", "port", port, "db", dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
