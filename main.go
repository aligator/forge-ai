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

	agents := make(map[string]service.Agent)
	forgejoClients := make(map[string]service.Forgejo)
	for i := range cfg.Agents {
		route := &cfg.Agents[i]
		key := strings.ToLower(route.Mention)
		agents[key] = agent.NewRunner(route.Agent, logger)

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
		forgejoClients[key] = forgejo.NewClient(cfg.ForgejoURL, token)
		logger.Info("registered agent", "mention", route.Mention, "user", route.User, "bin", route.Agent.Bin, "command", route.Agent.CommandTemplate)
	}

	forgejoClient := forgejo.NewClient(cfg.ForgejoURL, cfg.ForgejoToken)
	workflow := service.New(service.Options{
		Config:         cfg,
		Forgejo:        forgejoClient,
		ForgejoClients: forgejoClients,
		Git:            gitops.New(cfg.Git, logger),
		Agents:         agents,
		Logger:         logger,
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(cfg, workflow, logger),
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
