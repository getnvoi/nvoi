# Phase 4: Converge Local and Cloud Runtime Interpretation for Managed Databases and Agents

## Goal

Make local mode and cloud mode consume the same managed bundle compiler output and differ only in interpretation strategy:

- local executes immediately
- cloud resolves deployment steps and executes later

This phase makes managed databases and managed agents product capabilities in both contexts.

## Required outcome

Both modes consume the same `pkg/managed` output.

Cloud mode:

- resolves deployment steps via `plan.ResolveDeploymentSteps()`
- persists deployment steps
- executes them through the existing executor

Local mode:

- compiles bundles via `pkg/managed`
- executes primitive operations directly in deterministic order

Both modes must realize the same child resources, names, and dependency surfaces.

## Credential model

Managed services require specific credentials. The user provides them. If they are missing, the system raises a hard error.

There is no credential generation. There is no credential persistence. There is no local state file. There is no cluster credential pull.

The credential model is:

- each managed kind declares required credential keys
- credentials come from the user's env (local: `.env` / env vars / flags, cloud: `RepoConfig.Env`)
- if a required credential is missing, the system errors with a clear message naming the missing key
- no auto-generation, no magic, no implicit state

This applies to both local and cloud. The current cloud auto-generation path in `pkg/managed` and `RepoManagedServiceConfig` is replaced by this model.

What this kills:

- `Generated` / `GeneratedCreds` / `ResolvedCreds` on `managed.Result`
- `RepoManagedServiceConfig` table and all persistence of managed credentials in the DB
- `loadManagedState()` in handlers
- `cleanupManagedState()` in executor
- the `ExistingCredentials` field on `managed.Request`
- any credential lifecycle discussion

What replaces it:

- `managed.Request` takes a flat env map
- `managed.Compile()` validates required keys are present, errors if not
- the compiled bundle references credential values from the env directly
- handlers pass the parsed env to the compiler, same as they pass it to `plan.Build()`

## Intent

This phase removes the last behavioral split:

- cloud currently owns managed orchestration
- local currently has no managed entry surface

After this phase, managed databases and managed agents work in both contexts with one compiler and no credential magic.

## Directives

### 1. Remove credential generation from `pkg/managed`

The compiler must not generate credentials. It must require them.

Each managed kind declares its required credential keys. `Compile()` validates they are present in the provided env. Missing key = hard error with a message like:

```
managed postgres "db": missing required credential POSTGRES_PASSWORD (env: POSTGRES_PASSWORD)
```

### 2. Remove `RepoManagedServiceConfig` from cloud

The managed credential persistence table is deleted. Cloud handlers no longer load or store managed credentials separately. The env on `RepoConfig` is the single source for all credentials, managed or not.

### 3. Add local managed entry surfaces

Local mode must provide managed-aware commands so operators do not manually translate a managed declaration into raw primitive commands.

The local entry path must:

- compile the bundle via `pkg/managed`
- execute primitive operations in deterministic order via `pkg/core`

The execution loop is a simple iteration over `Bundle.Operations`, dispatching each to the corresponding `pkg/core` function.

### 4. Keep cloud mode on the same compiler

Cloud mode must consume the same compiler. Its cloud-specific responsibilities remain:

- step resolution via `plan.ResolveDeploymentSteps()`
- deployment record creation
- step persistence
- deferred execution

Cloud mode is an interpreter, not a managed-definition owner.

### 5. Make interpretation the only runtime difference

The repository must converge on this exact model:

- `pkg/managed` compiles
- local interprets by immediate execution
- cloud interprets by step persistence and later execution

No other layer is allowed to redefine:

- child resource names
- secret names
- exported dependency keys
- ownership topology
- operation ordering

### 6. Simplify `plan.ResolveDeploymentSteps()`

With credential generation removed, `ResolveDeploymentSteps` simplifies:

- no `NewCreds` in the result
- no `ManagedState` type (was only needed to carry persisted credentials)
- stored credentials no longer passed to the compiler — env is the single input
- the function still compiles bundles, strips managed-owned resources, merges with `Build()`, and handles removals

### 7. Align operator-facing visibility

Managed child resources must be visible consistently in both modes:

- describe output
- direct command output in local
- deployment step views in cloud

## Completion criteria

This phase is complete only when all of the following are true:

- local mode realizes managed postgres without manual primitive translation
- local mode realizes managed agent without manual primitive translation
- cloud mode uses the same compiler output
- credentials are required input, not generated
- `RepoManagedServiceConfig` table is removed
- local and cloud produce identical child topology
- parity lives at the compiler level via `pkg/managed` tests

## Required tests

Add tests that prove:

- `Compile()` errors on missing required credentials with a clear message
- local and cloud compile identical postgres bundles from the same env
- local and cloud compile identical agent bundles from the same env
- local and cloud produce identical child resource names
- local and cloud expose identical dependency exports
- deleting managed bundles removes the same owned resources in both contexts

## Naming Amendment

When referring to the shared step-building layer introduced before interpretation, use:

- `ResolveDeploymentSteps`

Do not describe that layer as `lowering` in new work.

The cloud path resolves deployment steps and persists them.
The local path compiles bundles and executes operations directly.
