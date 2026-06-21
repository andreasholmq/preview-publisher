package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/andreasholmqvist/preview-publisher/internal/auth"
	"github.com/andreasholmqvist/preview-publisher/internal/config"
	"github.com/andreasholmqvist/preview-publisher/internal/server"
	"github.com/andreasholmqvist/preview-publisher/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	if err := prepareDataDirs(cfg.DataDir); err != nil {
		logger.Error("prepare data directories", "error", err)
		os.Exit(1)
	}
	secret, err := auth.LoadOrCreateSecret(cfg.DataDir, cfg.CookieSecret)
	if err != nil {
		logger.Error("load cookie secret", "error", err)
		os.Exit(1)
	}
	signer, err := auth.NewSigner(secret)
	if err != nil {
		logger.Error("create cookie signer", "error", err)
		os.Exit(1)
	}
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(cfg, db, signer, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("preview gateway listening", "addr", cfg.ListenAddr)
		errs <- httpServer.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-signals:
		logger.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			logger.Error("shutdown failed", "error", err)
			os.Exit(1)
		}
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}
}

func prepareDataDirs(dataDir string) error {
	for _, dir := range []string{
		dataDir,
		filepath.Join(dataDir, "previews"),
		filepath.Join(dataDir, "secrets"),
		filepath.Join(dataDir, "tmp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func logLevel(raw string) slog.Level {
	switch raw {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
