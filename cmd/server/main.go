package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/httpapi"
	"multiagentcom/internal/service"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Auto-generate API token if none is configured.
	if strings.TrimSpace(cfg.APIToken) == "" && strings.TrimSpace(cfg.AuthTokens) == "" && strings.TrimSpace(cfg.AuthTokensFile) == "" {
		token, err := generateSecureToken(16) // 128-bit token
		if err != nil {
			logger.Error("failed to generate API token", "error", err)
			os.Exit(1)
		}
		cfg.APIToken = token
		logger.Info("═══════════════════════════════════════════════════════════════")
		logger.Info("  Auto-generated API Token (save this, it won't be shown again)")
		logger.Info(fmt.Sprintf("  Token: %s", token))
		logger.Info("═══════════════════════════════════════════════════════════════")
	}

	if err := config.Validate(cfg); err != nil {
		logger.Error("invalid configuration", "error", err, "issues", config.ValidationIssues(err))
		os.Exit(1)
	}

	svc := service.New(cfg, logger)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           httpapi.NewServer(cfg, logger, svc),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("server starting", "service", cfg.ServiceName, "addr", cfg.Address)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Cleanup sandboxes before shutdown.
	if err := svc.CleanupOnShutdown(shutdownCtx); err != nil {
		logger.Warn("sandbox cleanup on shutdown failed", "error", err)
	}

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

// generateSecureToken generates a cryptographically secure random token.
func generateSecureToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
