package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

type webhookHandler struct {
	cfg            config.Config
	forgejo        Forgejo
	forgejoClients map[string]Forgejo
	agents         map[string]Agent
	runtime        *appruntime.Runtime
	runner         *workflowRunner
	logger         *slog.Logger
}

func (h *webhookHandler) Handle(ctx context.Context, event string, payload forgejo.WebhookPayload) error {
	ticket, ok := forgejo.TicketFromPayload(event, payload)
	if !ok {
		h.logger.Info("ignored webhook without supported ticket", "event", event, "action", payload.Action)
		return nil
	}

	h.logger.Debug("ticket from payload",
		"ticket", ticket.Ref(),
		"title", ticket.Title,
		"instruction", ticket.Instruction,
		"comment_id", ticket.CommentID,
		"has_review", payload.Review != nil,
	)

	if ticket.Instruction == "" && event == "pull_request_comment" && payload.Action == "reviewed" {
		comments, err := h.forgejo.GetLatestPullReviewComments(ctx, ticket.Owner, ticket.Repo, ticket.Number)
		if err != nil {
			h.logger.Warn("fetch review comments failed", "error", err)
		} else {
			h.logger.Debug("fetched review comments", "count", len(comments))
			for _, c := range comments {
				if h.anyMentionIn(c.Body) && ticket.CommentID == 0 {
					ticket.CommentID = c.ID
				}
				if ticket.Instruction == "" {
					ticket.Instruction = c.Body
				} else {
					ticket.Instruction += "\n" + c.Body
				}
			}
		}
	}

	if payload.Action == "deleted" {
		h.logger.Info("ignored deleted webhook", "event", event, "ticket", ticket.Ref())
		return nil
	}

	if !h.shouldRun(payload, ticket) {
		sender := ""
		if payload.Sender != nil {
			sender = payload.Sender.Handle()
		}
		var mentions []string
		for _, route := range h.cfg.Agents {
			mentions = append(mentions, route.Mention)
		}
		h.logger.Info("ignored webhook without mention",
			"event", event,
			"action", payload.Action,
			"ticket", ticket.Ref(),
			"sender", sender,
			"bootstrap_user", h.cfg.ForgejoBootstrapUser,
			"instruction", fmt.Sprintf("%q", ticket.Instruction),
			"configured_mentions", mentions,
		)
		return nil
	}

	mention, ag := h.findAgent(ticket.Instruction)
	fc := h.forgejoFor(mention)
	route := h.routeForMention(mention)
	branch := branchForTicket(h.cfg, ticket)
	base := branchRef(firstNonEmpty(ticket.BaseBranch, ticket.DefaultBranch, "main"))
	run, err := h.runner.createRun(ctx, payload, ticket, mention, branch, base)
	if err != nil {
		return err
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	h.registerRunCancel(run.ID, runCancel)
	defer h.unregisterRunCancel(run.ID)
	defer runCancel()

	spec := appruntime.RunSpec{
		TicketRef: ticket.Ref(),
		Branch:    branch,
		Owner:     ticket.Owner,
		Repo:      ticket.Repo,
		Kind:      ticket.Kind,
		Number:    ticket.Number,
	}

	err = h.runtime.SubmitWebhookRun(runCtx, spec, func(runCtx context.Context) error {
		if err := postStartAckWith(runCtx, fc, ticket); err != nil {
			h.logger.Warn("post start acknowledgement failed", "comment_id", ticket.CommentID, "error", err)
		}
		return h.runner.run(runCtx, fc, ticket, ag, run, route.Git)
	})
	switch {
	case errors.Is(err, appruntime.ErrTicketActive):
		h.runner.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		h.logger.Info("ignored webhook, ticket already active", "ticket", ticket.Ref())
		return nil
	case errors.Is(err, appruntime.ErrBranchActive):
		h.runner.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		h.logger.Info("ignored webhook, branch already active", "ticket", ticket.Ref(), "branch", branch)
		return nil
	case errors.Is(err, context.Canceled):
		h.runner.finishRun(context.Background(), run.ID, runstore.StatusCanceled, err)
		return nil
	default:
		return err
	}
}

func (h *webhookHandler) registerRunCancel(id string, cancel context.CancelFunc) {
	if h == nil || h.runner == nil || h.runner.service == nil {
		return
	}
	h.runner.service.registerCancel(id, cancel)
}

func (h *webhookHandler) unregisterRunCancel(id string) {
	if h == nil || h.runner == nil || h.runner.service == nil {
		return
	}
	h.runner.service.unregisterCancel(id)
}

// forgejoFor returns the Forgejo client for the given mention, falling back to the global client.
func (h *webhookHandler) forgejoFor(mention string) Forgejo {
	if h.forgejoClients != nil {
		if fc, ok := h.forgejoClients[strings.ToLower(mention)]; ok {
			return fc
		}
	}
	return h.forgejo
}

func (h *webhookHandler) shouldRun(payload forgejo.WebhookPayload, ticket forgejo.Ticket) bool {
	lower := strings.ToLower(ticket.Instruction)
	for _, route := range h.cfg.Agents {
		if route.Disabled {
			continue
		}
		if !strings.Contains(lower, strings.ToLower(route.Mention)) {
			continue
		}
		if payload.Sender != nil {
			handle := payload.Sender.Handle()
			agentUser := route.User
			if agentUser == "" {
				agentUser = h.cfg.ForgejoBootstrapUser
			}
			if handle == agentUser {
				return false
			}
		}
		return true
	}
	return false
}

func (h *webhookHandler) anyMentionIn(text string) bool {
	lower := strings.ToLower(text)
	for _, route := range h.cfg.Agents {
		if route.Disabled {
			continue
		}
		if strings.Contains(lower, strings.ToLower(route.Mention)) {
			return true
		}
	}
	return false
}

// findAgent returns the matched mention and runner for the first mention found in instruction.
// Assumes shouldRun already confirmed a match exists.
func (h *webhookHandler) findAgent(instruction string) (string, Agent) {
	lower := strings.ToLower(instruction)
	for _, route := range h.cfg.Agents {
		if route.Disabled {
			continue
		}
		if strings.Contains(lower, strings.ToLower(route.Mention)) {
			key := strings.ToLower(route.Mention)
			if ag, ok := h.agents[key]; ok {
				return key, ag
			}
		}
	}
	return "", nil
}

func (h *webhookHandler) routeForMention(mention string) config.AgentRoute {
	for _, route := range h.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) {
			return route
		}
	}
	return config.AgentRoute{Git: h.cfg.Git.GitIdentity}
}
