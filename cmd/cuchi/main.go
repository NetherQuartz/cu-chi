package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/config"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/httpapp"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/worker"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		listen := os.Getenv("CUCHI_LISTEN")
		if listen == "" {
			listen = "127.0.0.1:8080"
		}
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			os.Exit(1)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/readyz")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := store.Open(startup, cfg.DatabaseURL)
	if err != nil {
		return errors.New("database startup failed; check connection and migration state")
	}
	defer db.Pool.Close()
	box, _ := cryptobox.New(cfg.MasterKey)
	vpnWorker := worker.Worker{DB: db, Box: box, Remote: deployer.SSH{}, Image: cfg.AWGImage}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); vpnWorker.Run(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()
	app, err := httpapp.New(startup, cfg, db)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() {
		slog.Info("Củ Chi listening", "url", cfg.PublicURL, "development_auth", cfg.DevAuth)
		done <- server.ListenAndServe()
	}()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return server.Shutdown(shutdown)
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
			cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := db.Cleanup(cleanup); err != nil {
				slog.Warn("expired-session cleanup failed")
			}
			cancel()
		}
	}
}
