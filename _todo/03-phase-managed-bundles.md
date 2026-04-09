# Phase 3: Rebuild Managed Databases and Coding Agents as Owned Resource Bundles

## Goal

Replace the single-service expansion model with complete managed bundles that own all primitive resources required to realize a managed database or managed agent workload.

This phase makes managed services concrete product bundles instead of shorthand service substitutions.

## Required outcome

Managed kinds compile into complete owned bundles of primitive operations that can include:

- `secret.set`
- `volume.set`
- `storage.set`
- `service.set`
- `cron.set`

The initial managed kinds in scope are:

- postgres
- agent

## Intent

This phase completes the managed resource model for the two prioritized surfaces:

- databases
- coding agents

Managed postgres must own:

- database workload
- data volume
- generated credentials
- exported dependency keys
- scheduled backup workload
- backup storage when backup storage is part of the managed bundle

Managed agent must own:

- agent runtime workload
- owned state volume where required
- generated credentials and access keys where required
- exported access keys for supported consumers

The point is not to make the system smart. The point is to make ownership explicit and stable.

## Directives

### 1. Define postgres as a complete managed database bundle

Managed postgres must compile to a bundle containing:

- primary database service workload
- primary data volume
- generated database credentials
- namespaced internal password secret
- exported dependency secrets for consumers
- backup cron workload
- backup target storage and credentials when backups are part of the bundle

The bundle must define:

- exact child names
- exact secret names
- exact exported dependency surface
- exact primitive operation ordering

### 2. Define agent as a complete managed workload bundle

Managed agent must compile to a bundle containing:

- main agent service workload
- owned state volume when the agent requires persistent runtime state
- generated internal credentials or tokens required by the runtime shape
- exported connection or access keys required by supported consumers

The agent bundle must remain narrow and concrete. It represents one deployable agent runtime shape, not a general platform.

### 3. Standardize dependency exports

Dependent workloads must consume managed resources through stable exported secret surfaces declared by the bundle.

For postgres:

- `DATABASE_DB_*`

For agent:

- one fixed exported namespace determined by the managed kind

Cloud and local mode must not rebuild these keys themselves.

### 4. Replace API-local expansion with bundle lowering

The old API-only flow:

- parse config
- replace `managed:` with concrete service spec
- inject dependency secrets

must be replaced by:

- parse config
- compile managed bundles
- lower bundles into primitive operations and dependent secret refs

The compiler is the source of truth. Downstream layers only lower and interpret.

### 5. Define deterministic lowering order

Lowering must follow one strict order:

1. generated secrets
2. storage prerequisites
3. volume prerequisites
4. primary service workloads
5. scheduled child workloads
6. dependent workload secret refs derived from managed exports

This order must be implemented once and tested.

### 6. Define ownership-driven delete behavior

Every managed bundle must declare its owned resources exactly.

When a managed resource is removed:

- all owned service workloads are deleted
- all owned cron workloads are deleted
- all owned volumes are deleted when the bundle owns them
- all owned storages are deleted when the bundle owns them
- all owned secrets are deleted

Delete behavior follows explicit ownership. It does not depend on name guessing outside the compiler.

## Completion criteria

This phase is complete only when all of the following are true:

- postgres compiles into a full database bundle
- postgres backup compiles to `cron.set`
- agent compiles into a full managed workload bundle
- dependency exports come from bundle definitions
- cloud lowering consumes bundles
- local lowering consumes bundles
- delete behavior follows bundle ownership

## Required tests

Add tests that prove:

- postgres bundle contains database workload, backup cron, volume, secrets, and dependency exports
- postgres child names are stable
- postgres operation ordering is stable
- agent bundle contains its owned workload, secrets, and volume where required
- agent child names are stable
- dependent workloads receive exactly the exported keys declared by the bundle
- removing a managed bundle removes all owned child resources
- bundle lowering is identical between cloud and local adapters before interpretation

## Strong directive

Do not keep two parallel managed models.

After this phase there is one managed model:

- `pkg/managed` compiles bundles
- runtimes interpret primitive operations

## Naming Amendment

The term `lowering` is no longer the preferred name for this phase output.

Use this naming instead:

- function intent: `ResolveDeploymentSteps`
- result intent: resolved deployment steps

Meaning:

- managed bundles plus plain config are translated into the final concrete nvoi steps to execute
- the output is the ordered executable step sequence
- the output also carries the stripped config and managed secret/export effects required to produce that sequence

Do not introduce new names like `program`, `lowered`, or `lowering` in future phase work when this same concept is meant.
