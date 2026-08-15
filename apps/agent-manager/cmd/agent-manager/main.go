package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent/codex"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/changeset"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/eventbroker"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	reviewservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/review"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/runtimeinfo"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/server"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	storesqlite "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("agent manager stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	host := flag.String("host", "127.0.0.1", "listen host")
	port := flag.Int("port", 3100, "listen port; use 0 for an ephemeral port")
	defaultDir, err := defaultDataDir()
	if err != nil {
		return err
	}
	dataDir := flag.String("data-dir", defaultDir, "directory for the database and runtime data")
	runtimeFile := flag.String("runtime-file", "", "path for runtime metadata; defaults to <data-dir>/runtime.json")
	authToken := flag.String("auth-token", "", "bearer token; generated when omitted")
	allowedOrigins := flag.String("allowed-origins", "http://localhost:5173,http://127.0.0.1:5173", "comma-separated browser origins")
	flag.Parse()
	if *runtimeFile == "" {
		*runtimeFile = filepath.Join(*dataDir, "runtime.json")
	}
	if *authToken == "" {
		*authToken, err = runtimeinfo.GenerateToken()
		if err != nil {
			return err
		}
	}

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	broker := eventbroker.New()
	store, err := storesqlite.Open(
		context.Background(),
		filepath.Join(*dataDir, "manager.db"),
		storesqlite.WithEventPublisher(broker),
	)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()
	worktreeManager, err := gitworktree.New()
	if err != nil {
		return err
	}
	sessions := sessionservice.New(store, worktreeManager, filepath.Join(*dataDir, "worktrees"))
	changeDetector, err := changeset.New()
	if err != nil {
		return err
	}
	runs := runservice.New(store, codex.New("codex"), runservice.WithChangeDetector(changeDetector))
	reviews := reviewservice.New(store, reviewservice.WithSessionCloser(sessions))
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = runs.Close(closeCtx)
	}()

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	if err := runtimeinfo.Write(*runtimeFile, runtimeinfo.Metadata{
		PID:           os.Getpid(),
		Address:       listener.Addr().String(),
		AuthToken:     *authToken,
		Version:       version,
		SchemaVersion: protocol.SchemaVersion,
		StartedAt:     time.Now().UTC(),
	}); err != nil {
		return err
	}
	defer os.Remove(*runtimeFile)

	httpServer := &http.Server{
		Handler: server.New(server.Config{
			Version:          version,
			SchemaVersion:    protocol.SchemaVersion,
			AuthToken:        *authToken,
			AllowedOrigins:   splitNonEmpty(*allowedOrigins),
			EventSubscriber:  broker,
			SessionCreator:   sessions,
			SessionCloser:    sessions,
			RunController:    runs,
			ChangeReader:     store,
			ReviewController: reviews,
		}, store, store).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("agent manager listening", "address", listener.Addr().String(), "version", version, "runtime_file", *runtimeFile)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runs.Close(shutdownCtx); err != nil {
			return fmt.Errorf("stop active runs: %w", err)
		}
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func defaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user data directory: %w", err)
	}
	return filepath.Join(base, "maatgen"), nil
}
