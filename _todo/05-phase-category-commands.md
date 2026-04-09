# Phase 5: Add Stable Category Commands for Databases and Agents

## Goal

Introduce stable operator-facing command groups for the two specialized categories being shipped:

- `database`
- `agent`

These command groups exist upfront as part of the product surface and dispatch only when the targeted service resolves to an existing managed resource of the correct category.

## Required outcome

The product exposes stable category command families that are always present in the CLI and API surface.

Initial command groups are:

- `database`
- `agent`

The initial command set includes:

- `database list`
- `database backup create <service>`
- `database backup list <service>`
- `database backup download <service> <artifact>`
- `agent list`
- `agent exec <service> -- <cmd>`
- `agent logs <service>`

The surface is fixed and discoverable. Availability is resolved at runtime against existing managed services.

## Intent

This phase turns managed bundles into operator capabilities.

The command groups are category-based, not kind-based.

That means:

- the product exposes `database` commands, not `postgres` commands
- the product exposes `agent` commands, not per-kind agent command families

This keeps the command surface coherent, documented, and stable.

The system stays deliberately narrow:

- one specialized database category
- one specialized agent category

No category explosion is introduced.

## Directives

### 1. Define category membership in managed definitions

Each managed kind in `pkg/managed` must declare one primary operator category.

In this phase:

- postgres belongs to `database`
- agent belongs to `agent`

This category membership is part of the managed definition.

### 2. Define category capability contracts

The product must define narrow capability contracts for each shipped category.

For `database`, define:

- list managed database services
- create backup artifact
- list backup artifacts
- download backup artifact

For `agent`, define:

- list managed agent services
- execute command in target agent workload
- stream or fetch logs

These capability contracts are product contracts. They are not ad hoc CLI wrappers.

### 3. Add stable command groups

The CLI must expose category groups upfront.

The groups are always present:

- `nvoi database ...`
- `nvoi agent ...`

They do not appear or disappear dynamically.

### 4. Resolve commands against existing services at runtime

Category commands must follow this resolution flow:

1. resolve the target service
2. verify that it exists
3. verify that it is managed
4. verify that its managed kind belongs to the requested category
5. dispatch the category capability

The rejection model is strict:

- target service missing -> not found
- target service not managed -> unsupported for this service
- target service managed but wrong category -> unsupported capability
- target service supports the category command -> execute

### 5. Add category-level listing commands

`database list` must enumerate all managed database services in the current app/environment.

`agent list` must enumerate all managed agent services in the current app/environment.

The output must identify:

- service name
- managed kind
- primary category
- enabled capabilities relevant to the category
- owned child topology summary where useful

### 6. Standardize artifact handling for database backups

Database backup commands must operate on a normalized backup artifact model.

Each artifact record must expose:

- artifact id
- service name
- created time
- size when available
- storage locator required for download

The operator surface is artifact-oriented. It does not expose raw storage implementation details as the command contract.

### 7. Keep the category set deliberately small

This phase ships only:

- `database`
- `agent`

That is sufficient for the two specialized managed surfaces now being prioritized.

The goal is clarity and utility, not taxonomy growth.

## Package boundary after completion

### `pkg/managed`

Owns:

- category membership for managed kinds
- capability dispatch contracts for `database` and `agent`

### category command layer

Owns:

- stable command definitions
- runtime resolution against existing managed services
- execution of category capabilities

### managed kind implementations

Own:

- category-specific execution details behind the capability contract

## Completion criteria

This phase is complete only when all of the following are true:

- `database` and `agent` command groups are exposed upfront
- postgres resolves under `database`
- agent resolves under `agent`
- `database list` enumerates managed databases
- `agent list` enumerates managed agent services
- database backup commands operate through a normalized artifact model
- wrong-category dispatch fails cleanly and predictably

## Required tests

Add tests that prove:

- `database list` returns all managed database services and no non-database services
- `agent list` returns all managed agent services and no non-agent services
- `database backup list` rejects non-managed services
- `database backup list` rejects wrong-category managed services
- `database backup download` resolves artifact metadata correctly
- `agent exec` dispatches only to managed agent services
- category membership comes from managed definitions, not duplicated lookup code

## Strong directive

Do not generate commands dynamically per kind.

The product exposes stable category commands upfront.

Managed kinds map into those commands through declared category membership and capability contracts.
