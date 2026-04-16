package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// AgentEventsInput is the request body for POST /agent/events.
type AgentEventsInput struct {
	Authorization string `header:"Authorization"` // agent token — own auth, not JWT
	Body          struct {
		App    string              `json:"app" required:"true"`
		Env    string              `json:"env" required:"true"`
		Events []AgentEventPayload `json:"events" required:"true"`
	}
}

// AgentEventPayload is one event in the batch.
type AgentEventPayload struct {
	Type    string          `json:"type"`
	Message string          `json:"message,omitempty"`
	Command string          `json:"command,omitempty"`
	Action  string          `json:"action,omitempty"`
	Name    string          `json:"name,omitempty"`
	Extra   json.RawMessage `json:"extra,omitempty"` // command event metadata (domains, volumes, etc.)
	Payload json.RawMessage `json:"payload,omitempty"`
	Ts      *time.Time      `json:"ts,omitempty"` // agent-side timestamp — preserves event ordering within batches
}

// AgentEventsOutput is the response for POST /agent/events.
type AgentEventsOutput struct {
	Body struct {
		Stored int `json:"stored"`
	}
}

// AgentEvents handles batched event ingestion from agents.
func AgentEvents(db *gorm.DB) func(context.Context, *AgentEventsInput) (*AgentEventsOutput, error) {
	return func(ctx context.Context, input *AgentEventsInput) (*AgentEventsOutput, error) {
		repo, ok := authenticateAgent(db, input.Authorization, input.Body.App, input.Body.Env)
		if !ok {
			return nil, huma.Error401Unauthorized("invalid agent token")
		}

		now := time.Now()
		events := make([]api.AgentEvent, 0, len(input.Body.Events))
		for _, ev := range input.Body.Events {
			var payload string
			if ev.Payload != nil {
				payload = string(ev.Payload)
			}
			var extra string
			if ev.Extra != nil {
				extra = string(ev.Extra)
			}
			ts := now
			if ev.Ts != nil {
				ts = *ev.Ts
			}
			events = append(events, api.AgentEvent{
				RepoID:    repo.ID,
				App:       input.Body.App,
				Env:       input.Body.Env,
				Type:      ev.Type,
				Message:   ev.Message,
				Command:   ev.Command,
				Action:    ev.Action,
				Name:      ev.Name,
				Extra:     extra,
				Payload:   payload,
				CreatedAt: ts,
			})
		}

		if len(events) > 0 {
			if err := db.CreateInBatches(events, 100).Error; err != nil {
				return nil, fmt.Errorf("store events: %w", err)
			}
		}

		out := &AgentEventsOutput{}
		out.Body.Stored = len(events)
		return out, nil
	}
}

// authenticateAgent validates the bearer token and returns the matching repo.
// Looks up the repo by the SHA-256 hash of the token (indexed, unique per repo),
// then verifies the request's app/env match. This avoids the ambiguity of
// name+env lookups across workspaces.
func authenticateAgent(db *gorm.DB, authHeader, app, env string) (*api.Repo, bool) {
	if authHeader == "" {
		return nil, false
	}
	const prefix = "Bearer "
	token, ok := strings.CutPrefix(authHeader, prefix)
	if !ok || token == "" {
		return nil, false
	}

	hash := api.HashToken(token)
	var repo api.Repo
	if err := db.Where("agent_token_hash = ?", hash).First(&repo).Error; err != nil {
		return nil, false
	}

	// Secondary check: the request's app/env must match the repo.
	if repo.Name != app || repo.Environment != env {
		return nil, false
	}
	return &repo, true
}
