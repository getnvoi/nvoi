# Phase 1: Establish `pkg/managed` as the Single Compiler for Managed Resource Bundles

## Goal

Create `pkg/managed` as the single shared compiler that turns managed-service declarations into deterministic bundles of primitive nvoi operations and exported dependency metadata.

This phase defines the shared managed foundation for both:

- managed databases
- managed agent workloads

This phase does not introduce operator category commands yet. It establishes the compiler and bundle model that every later phase uses.

## Required outcome

The repository must contain a pure `pkg/managed` package with all of the following characteristics:

- it does not import `internal/...`
- it does not know about API handlers, GORM, repos, or agent runtime persistence
- it does not execute runtime operations
- it does not plan deployment steps
- it does not read env vars
- it does not perform SSH, kubectl, or provider API calls
- it does not mutate API config structs
- it returns deterministic managed bundles

Each bundle must describe:

- the managed kind
- the root service name
- owned child resources
- generated credentials
- exported dependency keys
- primitive operations required to realize the managed resource

## Intent

Managed services are currently defined as an API-local expansion path. That architecture blocks normalization because:

- cloud owns the only managed-service compiler
- local mode has no shared managed interpretation path
- child resources belong to handlers instead of the product model
- generated credentials and naming rules are trapped in `internal/api`

This phase corrects that by moving managed intent into a shared package and representing each managed service as an owned bundle of primitive operations.

The compiler is intentionally narrow. It is not a dynamic policy engine. It owns concrete managed definitions for the product surfaces being prioritized:

- databases
- agent workloads

## Directives

### 1. Define a single managed compiler contract

`pkg/managed` must expose one compiler entry point that accepts:

- managed declaration
- service name
- previously persisted credentials for that service
- app/environment naming context required to generate child resources

It must return:

- a deterministic managed bundle
- newly generated credentials when persistence is empty

The compiler contract is pure and deterministic. Identical input produces identical output.

### 2. Define the managed bundle shape

The managed bundle must be explicit and complete. It must represent all resources owned by the managed service.

The bundle must contain:

- identity
  - managed kind
  - root service name
  - owned child names
- generated secrets
  - internal secrets used by owned workloads
  - exported secrets used by other workloads
- infrastructure requirements
  - volumes
  - storage
- workload requirements
  - service workloads
  - future cron workloads
- primitive operations
  - ordered `secret.set`, `volume.set`, `storage.set`, `service.set`

The bundle must be rich enough to represent:

- a managed postgres service
- a managed agent service

without changing the contract later.

### 3. Define primitive operation output

The compiler must emit primitive intent, not config mutation.

The operation set in this phase is:

- `secret.set`
- `volume.set`
- `storage.set`
- `service.set`

Each operation must contain:

- primitive kind
- stable name
- full params
- ownership metadata linking it to the managed root

Cloud mode will lower these operations into deployment steps.
Local mode will execute these operations directly in a later phase.

### 4. Move managed definitions into `pkg/managed`

Concrete implementations must live in `pkg/managed`.

The first two managed kinds are:

- managed postgres
- managed coding agent

Each implementation must define:

- child resource names
- generated credentials
- exported dependency keys
- owned primitive operations

The managed definitions must not:

- know about API config parsing
- know about DB rows
- know about cloud deployment handlers
- call runtime execution code

### 5. Separate credential generation from credential persistence

`pkg/managed` owns credential generation rules and secret naming rules.

It does not own persistence.

Cloud mode will persist credentials in the database.
Local mode will persist credentials through its own runtime state path in a later phase.

The boundary is strict:

- compiler generates credential maps
- callers load and store credential maps

### 6. Define stable naming rules in one place

Every managed kind must own a closed naming model.

For managed postgres named `db`, `pkg/managed` owns names such as:

- `db`
- `db-data`
- `db-backup`
- `db-backups`
- `DATABASE_DB_*`
- `POSTGRES_PASSWORD_DB`

For a managed coding agent named `coder`, `pkg/managed` owns names such as:

- `coder`
- `coder-data`
- exported credentials and connection keys under one fixed namespace

No handler, planner, or CLI command may reconstruct these names independently.

### 7. Keep the public managed declaration surface simple

This phase preserves a small and concrete public surface.

The product continues to support:

- `managed: postgres`
- `managed: <agent-kind>`
- `uses: [...]`

The goal is not to invent a generic managed schema. The goal is to move current and near-term managed definitions into a shared compiler.

## Package boundary after completion

### `pkg/managed`

Owns:

- registry
- compiler
- managed postgres definition
- managed agent definition
- child-resource naming
- generated credentials
- exported dependency keys
- primitive operation bundles

Does not own:

- DB persistence
- CLI commands
- API handlers
- kubectl application
- plan step persistence

### cloud adapter

Owns:

- loading/storing managed credentials in DB
- invoking the compiler
- lowering primitive operations into plan input

### local adapter

Owns:

- later interpretation of compiler output

## Completion criteria

This phase is complete only when all of the following are true:

- `pkg/managed` exists as the single source of truth
- postgres is defined in `pkg/managed`
- the managed agent kind is defined in `pkg/managed`
- generated credentials come from `pkg/managed`
- exported dependency keys come from `pkg/managed`
- child resource names come from `pkg/managed`
- cloud code consumes `pkg/managed` instead of API-local definitions
- no managed definition source of truth remains under `internal/api`

## Required tests

Add tests that prove:

- postgres compilation is deterministic
- agent compilation is deterministic
- existing credentials suppress regeneration
- generated secret names are stable
- exported dependency keys are stable
- child resource names are stable
- primitive operations are sorted and deterministic
- ownership metadata is complete

## Strong directive

Do not rebuild a generic framework.

This phase must produce a narrow, strong managed compiler for the product surfaces being shipped:

- database
- agent

That compiler is the foundation for the remaining phases.
