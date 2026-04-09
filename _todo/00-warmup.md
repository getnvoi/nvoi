# Warmup and Execution Protocol

## Purpose

This file defines how every phase in `_todo` is executed and validated.

The phase documents define the target state.
This file defines the operating protocol used to reach that target state with a separate execution agent.

## Phase list

The execution order is:

1. `01-phase-managed-contract.md`
2. `02-phase-cron-primitive.md`
3. `03-phase-managed-bundles.md`
4. `04-phase-runtime-convergence.md`
5. `05-phase-category-commands.md`
6. `06-phase-docs-and-examples.md`
7. `99-final-recap-and-monitoring.md`

No phase is skipped.
No phase is merged into another.
No phase is executed out of order.

## Mandatory protocol for every phase

Every phase requires:

1. warmup
2. confirmation after warmup
3. execution
4. execution report
5. validation
6. explicit greenlight before the next phase starts

This protocol is mandatory for every phase in the list above.

## Warmup

Before any execution work starts, the execution agent performs a warmup for the target phase.

The warmup must contain:

- the exact goal of the phase
- the exact files and packages it will touch
- the exact boundaries it will preserve
- the exact runtime behavior it will add or change
- the exact tests it will add or update
- the exact completion criteria it intends to satisfy from the phase document

The warmup is not execution.
The warmup is a concrete implementation briefing for approval.

## Confirmation after warmup

After the execution agent provides the warmup, the warmup is submitted here for validation.

The rule is strict:

- no execution starts before warmup confirmation
- the warmup is checked against the active phase document
- the warmup receives either greenlight or rejection

Greenlight means:

- the warmup matches the phase goal
- the touched scope is correct
- the implementation path is coherent
- the completion criteria are fully covered

Rejection means:

- the warmup is incomplete
- the scope is wrong
- the boundaries are wrong
- the implementation path misses required outputs

When rejected, the execution agent rewrites the warmup and resubmits it.

## Execution

Once greenlight is given, the execution agent performs the implementation for that phase only.

Execution must remain phase-scoped.

The execution agent does not:

- pull work from later phases
- skip required tests
- leave unresolved partial work inside the phase
- reinterpret the phase goal

## Execution report

After execution, the execution agent must provide a report.

The report must contain:

- files changed
- behavior added or changed
- tests added or updated
- test results
- any remaining gap against the active phase completion criteria

The report is mandatory.

## Validation after execution

Once execution is complete, the execution report is submitted here for validation.

The validation result is binary:

- validated
- rejected

Validated means:

- the phase goal is reached
- the completion criteria are satisfied
- the boundaries were respected
- the tests support the change

Rejected means:

- required behavior is missing
- scope drift occurred
- tests are missing or insufficient
- the implementation does not fully satisfy the phase document

When rejected, a rework order is issued against the missing pieces.
The execution agent reworks the phase and reports again.

## Gate to the next phase

No next phase starts until the current phase is explicitly validated.

The gating rule is strict:

- warmup approved
- execution performed
- report received
- report validated
- only then next phase begins

## Operating model with the execution agent

The operating model is:

1. the execution agent warms up a phase
2. the warmup is sent here
3. this validation layer gives greenlight or rejection
4. on greenlight, the execution agent executes
5. the execution agent reports
6. this validation layer validates or rejects the report
7. on validation, the next phase may begin

This file is the control protocol for that loop.

## Non-negotiable rule

A phase is not considered complete because code was written.

A phase is complete only when:

- its warmup was approved
- its execution was reported
- its report was validated against the active phase document

That rule applies to every phase in `_todo`.
