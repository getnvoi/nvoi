# Final Phase: Recap, Monitoring, and Validation Ledger

## Goal

Provide the final supervisory phase used to monitor the execution agent across the entire rollout.

This phase is the control point used to:

- recap phase goals
- verify that each phase was actually completed
- validate reports before the next phase begins
- reject incomplete work and send it back for rework

This file is used as the monitoring reference while another agent executes the implementation.

## Recap of the rollout goals

The rollout goals are:

1. define `pkg/managed` as the single compiler for managed bundles
2. add `cron.set` and `cron.delete` as first-class primitives
3. define managed postgres and managed agent as owned resource bundles
4. converge local and cloud on the same managed compiler with credentials as required input (no generation, no persistence)
5. expose stable category commands under `database` and `agent`
6. update docs and examples to match the implemented model

These six goals define the completed program of work.

## Monitoring responsibilities

This validation layer must check every phase against:

- the phase goal
- the directives
- the completion criteria
- the required tests or checks

The execution agent does the implementation work.
This layer judges whether the implementation actually satisfies the phase.

## Validation ledger

Track each phase with one status only:

- `pending`
- `warmup-approved`
- `executed-awaiting-validation`
- `validated`
- `rejected-for-rework`

The phases to track are:

- `01-phase-managed-contract.md`
- `02-phase-cron-primitive.md`
- `03-phase-managed-bundles.md`
- `04-phase-runtime-convergence.md`
- `05-phase-category-commands.md`
- `06-phase-docs-and-examples.md`

No phase advances straight from `pending` to `validated`.

## Warmup validation rules

When a warmup is submitted, validation must confirm:

- the scope matches the active phase only
- the touched files and packages make sense
- the implementation path satisfies the directives
- the tests and checks cover the completion criteria

If those conditions are satisfied, issue greenlight.

If they are not satisfied, reject the warmup and specify what must be fixed before execution.

## Execution report validation rules

When an execution report is submitted, validation must confirm:

- the changed files match the approved warmup
- the behavior matches the phase goal
- the completion criteria are satisfied
- the required tests or checks were completed
- no critical required item is missing

If those conditions are satisfied, mark the phase validated.

If they are not satisfied, reject the report and issue a concrete rework order.

## Rework protocol

When a phase is rejected:

- specify the missing or incorrect items exactly
- keep the phase active
- require the execution agent to rework only the unresolved items
- require a new execution report
- validate again before moving on

The next phase remains blocked until the current phase is validated.

## Final acceptance rule

The rollout is complete only when all tracked phases are in status:

- `validated`

Anything else means the rollout is still in progress.

## Strong directive

Use this file as the supervisory checklist while another agent performs the work.

The implementation agent executes.
This validation layer approves warmups, validates reports, rejects incomplete work, and controls progression to the next phase.

## Naming Amendment

For all future warmups, execution reports, and validations, use:

- `ResolveDeploymentSteps`

when describing the shared step-resolution layer that turns managed bundles plus plain config into concrete executable nvoi steps.

Do not approve new warmups that fall back to vague naming like:

- lowering
- lowered output
- program

unless the warmup also maps that wording back to `ResolveDeploymentSteps` explicitly.
