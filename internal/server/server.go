package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
)

type Workflow interface {
	Handle(context.Context, string, forgejo.WebhookPayload) error
}

func New(cfg config.Config, workflow Workflow, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /webhook", handleWebhook(cfg, workflow, logger))
	return mux
}

func handleWebhook(cfg config.Config, workflow Workflow, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		if err := verifySignature(r, body, cfg.WebhookSecret); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var payload forgejo.WebhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		payload.Raw = append([]byte(nil), body...)

		event := firstHeader(r, "X-Forgejo-Event", "X-Gitea-Event")
		if event == "" {
			event = "unknown"
		}

		go func() {
			if err := workflow.Handle(context.Background(), event, payload); err != nil {
				logger.Error("workflow failed", "event", event, "error", err)
			}
		}()

		w.WriteHeader(http.StatusAccepted)
	}
}

func verifySignature(r *http.Request, body []byte, secret string) error {
	if secret == "" {
		return nil
	}

	signature := firstHeader(r, "X-Hub-Signature-256", "X-Gitea-Signature")
	if signature == "" {
		return errors.New("missing signature")
	}

	signature = strings.TrimPrefix(signature, "sha256=")
	actual, err := hex.DecodeString(signature)
	if err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	if !hmac.Equal(actual, expected) {
		return errors.New("signature mismatch")
	}
	return nil
}

func firstHeader(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := r.Header.Get(key); value != "" {
			return value
		}
	}
	return ""
}
