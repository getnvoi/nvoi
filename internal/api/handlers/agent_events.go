package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// AgentEventsInput is the request body for POST /agent/events.
type AgentEventsInput struct {
	// Auth is via a separate agent token header, not the JWT middleware.
	Header struct {
		Authorization string `header:"Authorization"`
	}
	Body struct {
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
	Payload json.RawMessage `json:"payload,omitempty"`
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
		repo, ok := authenticateAgent(db, input.Header.Authorization, input.Body.App, input.Body.Env)
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
			events = append(events, api.AgentEvent{
				RepoID:    repo.ID,
				App:       input.Body.App,
				Env:       input.Body.Env,
				Type:      ev.Type,
				Message:   ev.Message,
				Command:   ev.Command,
				Action:    ev.Action,
				Name:      ev.Name,
				Payload:   payload,
				CreatedAt: now,
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
// The agent sends the plaintext token (from NVOI_API_TOKEN env var), the API
// hashes it and compares against the stored AgentTokenHash on the repo.
func authenticateAgent(db *gorm.DB, authHeader, app, env string) (*api.Repo, bool) {
	if authHeader == "" {
		return nil, false
	}
	const prefix = "Bearer "
	if len(authHeader) <= len(prefix) {
		return nil, false
	}
	token := authHeader[len(prefix):]
	if token == "" {
		return nil, false
	}

	var repo api.Repo
	if err := db.Where("name = ? AND environment = ?", app, env).First(&repo).Error; err != nil {
		return nil, false
	}

	if !api.ValidateAgentToken(token, repo.AgentTokenHash) {
		return nil, false
	}
	return &repo, true
}
