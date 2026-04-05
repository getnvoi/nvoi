SHELL := /bin/bash
.DEFAULT_GOAL := help

# Capture everything after the target as ARGS.
# Usage: make cli instance list  /  make cloud login  /  make cloud push --config nvoi.yaml
ARGS = $(filter-out $@,$(MAKECMDGOALS))

# ── Dev ────────────────────────────────────────────────────────────────

.PHONY: test
test: ## Run vet + tests
	docker compose run --rm core sh -c 'go vet ./... && go test ./...'

.PHONY: build
build: ## Build all packages
	docker compose run --rm core go build ./...

# ── Direct CLI (core) ─────────────────────────────────────────────────

.PHONY: cli
cli: ## Run direct CLI (make cli instance list)
	docker compose run --rm core $(ARGS)

# ── Cloud CLI ─────────────────────────────────────────────────────────

.PHONY: cloud
cloud: ## Run cloud CLI (make cloud login)
	docker compose run --rm cli $(ARGS)

# ── API ��──────────────────────────────────────────────────────────────

.PHONY: api
api: ## Start the API server
	docker compose up api

.PHONY: api-down
api-down: ## Stop all services
	docker compose down

# ── Setup ─────────────��───────────────────────────────────────────────

.PHONY: provision
provision: ## Build images, start postgres + api
	docker compose build
	docker compose up -d postgres
	@echo "waiting for postgres..."
	@until docker compose exec postgres pg_isready -U nvoi > /dev/null 2>&1; do sleep 1; done
	docker compose up -d api

# ── Help ──────────────────────��───────────────────────────���───────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# Swallow extra args so make doesn't treat them as targets.
%:
	@:
