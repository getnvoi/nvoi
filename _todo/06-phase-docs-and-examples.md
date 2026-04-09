# Phase 6: Update Documentation and Examples to Match the Managed Database and Agent Model

## Goal

Update the repository documentation and examples so they reflect the managed runtime model introduced by the previous phases.

This phase makes the product surface legible, testable, and operable by others.

Most examples in this phase will use the `database` category, because database workflows are the primary specialized path being shipped first.

## Required outcome

The documentation and examples must match the implementation exactly.

The updated material must cover:

- managed databases
- managed agents
- `cron` as a first-class primitive
- category commands
  - `database ...`
  - `agent ...`
- local/cloud convergence for managed bundles

## Intent

The architecture change is only complete when the repository explains it clearly and demonstrates it concretely.

This phase ensures:

- the docs describe the real runtime model
- examples demonstrate the real product surface
- database workflows are represented end-to-end
- agent workflows are represented explicitly

The examples are part of the deliverable, not optional follow-up work.

## Directives

### 1. Update architecture documentation

All architecture-facing docs must be updated to reflect:

- `pkg/managed` as the single compiler
- managed bundles as owned resource graphs
- `cron.set` as a first-class primitive
- local/cloud as two interpreters of the same managed bundle output
- category commands under `database` and `agent`

The documentation must not describe the old API-only managed expansion model as the active design.

### 2. Update operator documentation

User-facing docs must describe:

- how managed databases are declared
- how managed agents are declared
- how `database list` works
- how `database backup create|list|download` works
- how `agent list` works
- how `agent exec` and `agent logs` work

The command grammar must be shown exactly as implemented.

### 3. Update examples

Examples must be revised so they exercise the current managed model.

The example set must include:

- managed database examples
- managed agent examples
- category command examples for `database`
- category command examples for `agent`

Most examples in this phase will use databases, especially:

- managed postgres declaration
- backup creation
- backup listing
- backup download

### 4. Keep database examples first-class

Database examples are the primary specialized path for this phase.

The example set must make database workflows obvious and complete.

That includes:

- declaration
- deployment
- backup operations
- local and cloud usage patterns where the product surface differs only by interpretation mode

### 5. Keep agent examples concrete and narrow

Agent examples must demonstrate the shipped agent model, not a hypothetical general platform.

The example set must show:

- managed agent declaration
- deployment
- listing
- execution
- logs

### 6. Align docs and examples with actual tests

The examples must remain aligned with the repository test and runtime model.

The docs must not advertise behavior that is not implemented.

## Completion criteria

This phase is complete only when all of the following are true:

- architecture docs describe the new managed compiler and runtime model
- operator docs describe `database` and `agent` category commands
- examples cover managed postgres workflows
- examples cover managed agent workflows
- examples reflect the actual command syntax and resource model
- no active doc still presents the old API-only managed design as current

## Required checks

Validate all of the following:

- command syntax in docs matches implemented CLI syntax
- database examples use the actual managed database flow
- agent examples use the actual managed agent flow
- docs mention `cron.set` as part of managed database backup realization
- docs explain local/cloud interpretation consistently

## Strong directive

Documentation and examples are part of the implementation.

This phase is required to complete the rollout.
