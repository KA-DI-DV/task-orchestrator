package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"orchestrator/internal/agent"
	"orchestrator/internal/api"
	"orchestrator/internal/config"
	"orchestrator/internal/jira"
	"orchestrator/internal/pipeline"
	"orchestrator/internal/storage"
	"orchestrator/internal/workspace"
)

// App збирає всі частини програми докупи ("wiring").
// Це єдине місце, де створюються залежності і передаються одна одній.
type App struct {
	Config   *config.Config
	Store    *storage.Store
	Pipeline *pipeline.Pipeline
}

func newApp(ctx context.Context, configPath string) (*App, error) {
	// slog — стандартний структурований логер Go.
	// Виводить рядки типу: time=... level=INFO msg="етап" key=PROJ-1 status=IN_DEV
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	store, err := storage.Connect(ctx, cfg.DatabaseURL())
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		return nil, err
	}

	runner := &agent.Runner{
		Binary:  "claude",
		Timeout: cfg.Orchestrator.AgentTimeout,
		// Журнал дій агентів: /tmp/workspaces/.logs/<KEY>.log (дивитися: task watch KEY=...).
		LogDir: filepath.Join(cfg.Orchestrator.WorktreesDir, ".logs"),
		// Ліміти підписки Claude з кожного запуску — в БД, адмінка показує їх у бічній панелі.
		OnRateLimits: func(info json.RawMessage) {
			if err := store.SaveClaudeLimits(context.Background(), info); err != nil {
				slog.Warn("не вдалося зберегти ліміти Claude", "error", err)
			}
		},
	}
	ws := &workspace.Manager{
		ReposBaseDir: cfg.Orchestrator.ReposBaseDir,
		WorktreesDir: cfg.Orchestrator.WorktreesDir,
	}

	// З Jira працює сам Claude Code через Jira MCP (роль "jira" у config.yaml).
	jiraRole := cfg.Roles["jira"]
	jiraClient := &jira.MCPClient{
		Runner:            runner,
		WorkDir:           cfg.Orchestrator.WorktreesDir,
		Flags:             jiraRole.CLIFlags,
		Model:             jiraRole.Model,
		SystemInstruction: jiraRole.SystemInstruction,
	}

	return &App{
		Config: cfg,
		Store:  store,
		Pipeline: &pipeline.Pipeline{
			Store:           store,
			Jira:            jiraClient,
			Workspace:       ws,
			Runner:          runner,
			Roles:           cfg.Roles,
			MaxReviewCycles: cfg.Orchestrator.MaxReviewCycles,
			CleanupWorktree: cfg.Orchestrator.CleanupWorktree,
			MainRepo:        cfg.Orchestrator.MainRepo,
			ExcludeRepos:    cfg.Orchestrator.ExcludeRepos,
			WorktreeCopy:    cfg.Orchestrator.WorktreeCopy,
			TestEnvScript:   cfg.Orchestrator.TestEnvScript,
			TestEnvCleanup:  cfg.Orchestrator.TestEnvCleanup,
		},
	}, nil
}

func (a *App) Close() {
	a.Store.Close()
}

// Serve запускає HTTP-сервер і коректно зупиняє його при Ctrl+C / docker stop.
func (a *App) Serve(ctx context.Context) error {
	server := api.NewServer(ctx, a.Pipeline, a.Store, a.Pipeline.Runner.LogDir)
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", a.Config.Server.Port),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Запускаємо сервер в окремій goroutine, бо ListenAndServe блокує виконання.
	// Помилку передаємо назад через канал (channel) — так goroutine спілкуються в Go.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("REST API слухає", "addr", httpServer.Addr)
		errCh <- httpServer.ListenAndServe()
	}()

	// select чекає на те, що станеться першим: сигнал зупинки або помилка сервера.
	select {
	case <-ctx.Done():
		slog.Info("зупиняю сервер...")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}

	// Фонові задачі отримали скасований ctx і зупиняються. Їхній статус
	// лишився в БД — після перезапуску їх можна продовжити через resume.
	server.Wait()
	return nil
}
