package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/gitops"
	"codeberg.org/forge-ai/internal/runstore"
	"codeberg.org/forge-ai/internal/server"
	"codeberg.org/forge-ai/internal/service"
)

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	if cfg.ForgejoToken == "" && cfg.ForgejoBootstrapEnabled {
		token, err := forgejo.GenerateAccessToken(context.Background(), cfg.ForgejoURL, cfg.ForgejoBootstrapUser, cfg.ForgejoBootstrapPass, cfg.ForgejoBootstrapToken)
		if err != nil {
			logger.Error("bootstrap forgejo token", "error", err)
			os.Exit(1)
		}
		cfg.ForgejoToken = token
		logger.Info("generated forgejo dev token", "user", cfg.ForgejoBootstrapUser)
	}

	store, err := runstore.OpenSQLite(context.Background(), cfg.RunStorePath)
	if err != nil {
		logger.Error("open runstore", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("close runstore", "error", err)
		}
	}()
	logger.Info("runstore ready", "path", cfg.RunStorePath)
	if err := applyStoredAgentSettings(context.Background(), &cfg, store); err != nil {
		logger.Error("load agent settings", "error", err)
		os.Exit(1)
	}

	agents := make(map[string]service.Agent)
	forgejoClients := make(map[string]service.Forgejo)
	for i := range cfg.Agents {
		route := &cfg.Agents[i]
		if route.Disabled {
			logger.Info("agent disabled by settings", "mention", route.Mention, "user", route.User)
			continue
		}
		key := strings.ToLower(route.Mention)

		if route.Token == "" && route.User != "" && route.Password != "" {
			tok, err := forgejo.GenerateAccessToken(context.Background(), cfg.ForgejoURL, route.User, route.Password, "forge-ai")
			if err != nil {
				logger.Warn("could not bootstrap agent token", "user", route.User, "error", err)
			} else {
				route.Token = tok
				logger.Info("bootstrapped agent token", "user", route.User)
			}
		}

		token := route.Token
		if token == "" {
			token = cfg.ForgejoToken
		}
		route.Agent.ExtraEnv = []string{
			"FORGEJO_ACCESS_TOKEN=" + token,
			"FORGEJO_URL=" + cfg.ForgejoURL,
			"GIT_AUTHOR_NAME=" + route.Git.UserName,
			"GIT_AUTHOR_EMAIL=" + route.Git.UserEmail,
			"GIT_COMMITTER_NAME=" + route.Git.UserName,
			"GIT_COMMITTER_EMAIL=" + route.Git.UserEmail,
		}
		agents[key] = agent.NewRunner(route.Agent, logger)
		forgejoClients[key] = forgejo.NewClient(cfg.ForgejoURL, token)
		logger.Info("registered agent", "mention", route.Mention, "user", route.User, "bin", route.Agent.Bin, "command_configured", route.Agent.CommandTemplate != "")
	}

	forgejoClient := forgejo.NewClient(cfg.ForgejoURL, cfg.ForgejoToken)
	workflow := service.New(service.Options{
		Config:         cfg,
		Forgejo:        forgejoClient,
		ForgejoClients: forgejoClients,
		Git:            gitops.New(cfg.Git, logger),
		Agents:         agents,
		RunStore:       store,
		Logger:         logger,
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(cfg, workflow, store, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("forge-ai listening", "addr", cfg.HTTPAddr)
		errs <- httpServer.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
}

func applyStoredAgentSettings(ctx context.Context, cfg *config.Config, store *runstore.SQLiteStore) error {
	settings, err := store.ListAgentSettings(ctx)
	if err != nil {
		return err
	}
	byMention := make(map[string]runstore.AgentSettings, len(settings))
	for _, item := range settings {
		byMention[strings.ToLower(item.Mention)] = item
	}
	for i := range cfg.Agents {
		route := &cfg.Agents[i]
		setting, ok := byMention[strings.ToLower(route.Mention)]
		if !ok {
			continue
		}
		route.Disabled = !setting.Enabled
		route.Agent.Model = setting.Model
		route.Agent.Args = append([]string(nil), setting.Args...)
		route.Agent.Timeout = setting.Timeout
		route.Agent.ToolHints = setting.ToolHints
		if setting.AllowGitSet {
			route.Agent.AllowGit = setting.AllowGit
			route.Agent.AllowGitSet = true
		}
	}
	return nil
}
