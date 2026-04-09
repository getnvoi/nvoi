# Phase 2: Introduce `cron.set` and `cron.delete` as First-Class nvoi Primitives

## Goal

Add a first-class cron workload primitive so managed databases can own scheduled backup workloads through the same runtime vocabulary in both local and cloud mode.

This phase is database-driven. The immediate product reason for the cron primitive is scheduled backup execution for managed postgres.

## Required outcome

The repository must support:

- `pkg/core.CronSet`
- `pkg/core.CronDelete`
- CLI commands `nvoi cron set` and `nvoi cron delete`
- cloud plan step kinds `cron.set` and `cron.delete`
- executor dispatch for those step kinds
- Kubernetes CronJob manifest generation
- describe/inspection support for cron workloads
- idempotent create/update/delete behavior

## Intent

Kubernetes remains the scheduler.

The purpose of this phase is not to introduce a second scheduling model. The purpose is to give nvoi a stable primitive for scheduled workloads so managed databases can compile to first-class runtime operations instead of hidden raw manifests.

`cron.set` is the scheduled-workload equivalent of `service.set`.

## Directives

### 1. Define the cron runtime contract in `pkg/core`

Add:

- `CronSet`
- `CronDelete`

The request contract must support:

- cluster target
- workload name
- image
- command
- env vars
- secret refs
- volume mounts
- storage refs
- schedule
- target server

The runtime must:

- validate input strictly
- reuse existing secret aliasing rules
- reuse existing storage secret expansion rules
- reuse existing volume resolution rules
- apply CronJob resources idempotently

### 2. Add CronJob manifest generation under `pkg/kube`

The Kubernetes manifest layer must generate CronJob resources using the same product conventions already used for services:

- shared nvoi labels
- secret alias support
- named-volume and bind-mount support
- node selector support
- shell command override support

This phase does not define generic workload polymorphism. It adds one concrete primitive: cron.

### 3. Add CLI commands under `internal/core`

Add:

- `nvoi cron set`
- `nvoi cron delete`

The commands must follow existing nvoi command behavior:

- provider resolution
- app/env targeting
- output formatting
- error handling

### 4. Extend the cloud step model

Add:

- `cron.set`
- `cron.delete`

The executor must dispatch them through the same step execution path used for all other primitives.

No side execution path is allowed.

### 5. Extend describe and inspection

The product must show scheduled workloads explicitly.

Describe/inspection surfaces must expose:

- cron workload name
- schedule
- image
- age
- status information available from Kubernetes

Cron workloads are visible runtime resources. They are not hidden implementation details.

### 6. Align delete behavior with nvoi guarantees

`cron delete` must be idempotent.

Deleting a missing cron workload succeeds silently through the same not-found handling principles used elsewhere in nvoi.

## Completion criteria

This phase is complete only when all of the following are true:

- `nvoi cron set` applies a CronJob-backed workload
- `nvoi cron delete` removes it idempotently
- cloud planning emits `cron.set` and `cron.delete`
- cloud execution runs those steps
- describe/inspection surfaces include cron workloads
- cron manifests follow nvoi labels and secret conventions

## Required tests

Add tests that prove:

- cron manifests include correct labels and schedule
- cron workloads consume secret aliases correctly
- cron workloads consume storage refs correctly
- cron workloads mount named volumes correctly
- cron workloads support node selection
- `cron delete` is idempotent
- planner emits deterministic cron step ordering
- executor dispatches cron steps correctly

## Strong directive

This phase produces a complete first-class primitive.

Managed databases will depend on `cron.set`.
`cron.set` does not depend on managed databases.
