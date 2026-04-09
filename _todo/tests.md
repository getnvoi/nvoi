# Test plan for shared command tree + two backends

Tests organized by layer. Each section lists tests to add, update, or delete.

---

## pkg/commands/ — shared command tests (NEW)

Test that cobra commands parse flags correctly and call the right Backend method with the right arguments. Use a mock Backend.

### pkg/commands/commands_test.go — mock backend + helpers

```
MockBackend — records every call with arguments, returns configurable errors
assertCalled(t, mock, "InstanceSet", expectedArgs)
runCmd(t, cmd, args...) — executes cobra command with args, returns error
```

### pkg/commands/instance_test.go

```
TestInstanceSet_ParsesFlags — "set master --compute-type cx23 --compute-region fsn1" → InstanceSet("master", "cx23", "fsn1", false)
TestInstanceSet_Worker — "set worker-1 --compute-type cx33 --compute-region fsn1 --worker" → InstanceSet("worker-1", "cx33", "fsn1", true)
TestInstanceSet_MissingName — no args → cobra error
TestInstanceDelete_ParsesName — "delete master" → InstanceDelete("master")
TestInstanceDelete_MissingName — no args → cobra error
```

### pkg/commands/firewall_test.go

```
TestFirewallSet_Preset — "set cloudflare" → FirewallSet("cloudflare", nil)
TestFirewallSet_MissingPreset — no args → cobra error
```

### pkg/commands/volume_test.go

```
TestVolumeSet_ParsesFlags — "set pgdata --size 30 --server master" → VolumeSet("pgdata", 30, "master")
TestVolumeSet_MissingSize — missing --size → error
TestVolumeDelete_ParsesName — "delete pgdata" → VolumeDelete("pgdata")
```

### pkg/commands/storage_test.go

```
TestStorageSet_AllFlags — "set assets --cors --expire-days 30" → StorageSet("assets", true, 30)
TestStorageSet_Defaults — "set assets" → StorageSet("assets", false, 0)
TestStorageDelete_ParsesName — "delete assets" → StorageDelete("assets")
TestStorageEmpty_ParsesName — "empty assets" → StorageEmpty("assets")
```

### pkg/commands/service_test.go

```
TestServiceSet_FullFlags — all flags → ServiceSet with correct ServiceOpts
TestServiceSet_MinimalFlags — "--image nginx --port 80" → ServiceSet with defaults
TestServiceSet_SecretRejectsAlias — "--secret KEY=VALUE" → error
TestServiceDelete_ParsesName — "delete web" → ServiceDelete("web")
```

### pkg/commands/cron_test.go

```
TestCronSet_ParsesFlags — "--image busybox --schedule '0 1 * * *' --command 'echo hi'" → CronSet with correct CronOpts
TestCronSet_MissingImage — error
TestCronSet_MissingSchedule — error
TestCronDelete_ParsesName — "delete backup" → CronDelete("backup")
```

### pkg/commands/database_test.go

```
TestDatabaseSet_ParsesFlags — "set db --type postgres --secret KEY --backup-storage s --backup-cron '0 2 * * *'" → DatabaseSet with correct DatabaseOpts
TestDatabaseSet_MissingType — error "Available database types: postgres"
TestDatabaseSet_CustomImage — "--image postgres:16" → DatabaseOpts.Image = "postgres:16"
TestDatabaseDelete_ParsesFlags — "delete db --type postgres" → DatabaseDelete("db", "postgres")
TestDatabaseDelete_MissingType — error
TestDatabaseList — "list" → DatabaseList()
TestBackupCreate — "backup create db --type postgres" → BackupCreate("db", "postgres")
TestBackupList — "backup list db --type postgres" → BackupList("db", ...)
TestBackupDownload — "backup download db --type postgres key.sql.gz" → BackupDownload("db", ..., "key.sql.gz")
```

### pkg/commands/agent_test.go

```
TestAgentSet_ParsesFlags — "set coder --type claude --secret KEY" → AgentSet with correct AgentOpts
TestAgentSet_MissingType — error "Available agent types: claude"
TestAgentDelete_ParsesFlags — "delete coder --type claude" → AgentDelete("coder", "claude")
TestAgentList — "list" → AgentList()
TestAgentExec — "exec coder --type claude -- bash" → AgentExec("coder", "claude", ["bash"])
TestAgentLogs — "logs coder --type claude -f --tail 100" → AgentLogs("coder", "claude", LogsOpts{Follow: true, Tail: 100})
```

### pkg/commands/secret_test.go

```
TestSecretSet — "set KEY VALUE" → SecretSet("KEY", "VALUE")
TestSecretSet_MissingArgs — less than 2 args → error
TestSecretDelete — "delete KEY" → SecretDelete("KEY")
TestSecretList — "list" → SecretList()
TestSecretReveal — "reveal KEY" → SecretReveal("KEY")
```

### pkg/commands/dns_test.go

```
TestDNSSet_SingleRoute — "set web:example.com" → DNSSet([{web, [example.com]}], false)
TestDNSSet_MultiRoute — "set web:a.com api:b.com" → DNSSet([{web,[a.com]}, {api,[b.com]}], false)
TestDNSSet_CloudflareManaged — "set web:a.com --cloudflare-managed" → DNSSet(..., true)
TestDNSSet_MultiDomain — "set web:a.com,b.com" → DNSSet([{web, [a.com, b.com]}], false)
TestDNSDelete_SingleRoute — "delete web:a.com" → DNSDelete([{web, [a.com]}])
TestDNSDelete_MissingArg — no args → error
```

### pkg/commands/ingress_test.go

```
TestIngressSet_Default — "set web:a.com" → IngressSet({web,[a.com]}, false, "", "")
TestIngressSet_CloudflareManaged — "set web:a.com --cloudflare-managed" → IngressSet(..., true, "", "")
TestIngressSet_CustomCert — "set web:a.com --cert C --key K" → IngressSet(..., false, "C", "K")
TestIngressDelete_Default — "delete web:a.com" → IngressDelete({web,[a.com]}, false)
TestIngressDelete_CloudflareManaged — "delete web:a.com --cloudflare-managed" → IngressDelete(..., true)
```

### pkg/commands/build_test.go

```
TestBuild_SingleTarget — "--target web:./src" → Build({"web": "./src"})
TestBuild_MultiTarget — "--target web:./src --target api:./api" → Build({"web":"./src","api":"./api"})
TestBuildLatest — "latest web" → BuildLatest("web")
TestBuildPrune — "prune web --keep 3" → BuildPrune("web", 3)
```

### pkg/commands/describe_test.go

```
TestDescribe — runs → Describe()
```

### pkg/commands/logs_test.go

```
TestLogs_Default — "logs web" → Logs("web", LogsOpts{Tail: 50})
TestLogs_AllFlags — "logs web -f -n 100 --since 5m --previous --timestamps" → Logs("web", LogsOpts{Follow:true, Tail:100, Since:"5m", Previous:true, Timestamps:true})
```

### pkg/commands/exec_test.go

```
TestExec — "exec web -- bash -l" → Exec("web", ["bash", "-l"])
TestExec_MissingCommand — "exec web" → error
```

### pkg/commands/ssh_test.go

```
TestSSH — "ssh 'uptime'" → SSH("uptime")
```

### pkg/commands/resources_test.go

```
TestResources — runs → Resources()
```

### pkg/commands/deploy_test.go

```
TestDeploy — runs → Deploy()
```

---

## internal/core/backend_test.go — direct backend (NEW)

Test that DirectBackend correctly translates Backend method calls to pkg/core function calls. Uses mock SSH (same Tier 2 pattern as existing executor tests).

```
TestDirectBackend_InstanceSet — calls app.ComputeSet with correct request
TestDirectBackend_ServiceSet — calls app.ServiceSet with correct request
TestDirectBackend_DatabaseSet — reads secrets from cluster, compiles bundle, executes operations
TestDirectBackend_DatabaseDelete — calls Shape, deletes owned resources
TestDirectBackend_SecretSet — calls app.SecretSet
TestDirectBackend_IngressSet_CloudflareManaged — calls app.IngressSet with correct DNS ref
TestDirectBackend_EnsureBackupImage — checks registry, builds if missing
```

---

## internal/cli/backend_test.go — cloud backend (NEW)

Test that CloudBackend correctly mutates config and pushes. Uses httptest server (same Tier 3 pattern as existing handler tests).

```
TestCloudBackend_InstanceSet — GET config → add server → POST config with updated servers map
TestCloudBackend_InstanceDelete — GET config → remove server → POST config
TestCloudBackend_ServiceSet — adds service to config with all opts mapped
TestCloudBackend_DatabaseSet — adds managed service to config
TestCloudBackend_SecretSet — appends KEY=VALUE to env string
TestCloudBackend_SecretDelete — removes KEY from env string
TestCloudBackend_DNSSet — adds domain to config
TestCloudBackend_DNSSet_CloudflareManaged — sets ingress.cloudflare-managed: true
TestCloudBackend_IngressSet_CloudflareManaged — sets ingress.cloudflare-managed: true
TestCloudBackend_Deploy — POST /deploy + polls status
TestCloudBackend_DatabaseList — GET /database → returns list
TestCloudBackend_BackupCreate — POST /database/db/backup/create → waits for completion
TestCloudBackend_AgentExec — POST /agent/coder/exec → streams response
```

---

## Existing tests to UPDATE

### internal/api/config/schema_test.go

```
UPDATE: TestIngressConfig_* — use new CloudflareManaged bool instead of Exposure/TLS/Edge
ADD: TestIngressConfig_CloudflareManaged — parses { cloudflare-managed: true }
ADD: TestIngressConfig_CustomCert — parses { cert: X, key: Y }
ADD: TestIngressConfig_Default — omitted ingress = ACME
ADD: TestIngressConfig_InvalidBothCFAndCert — cloudflare-managed + cert = error
ADD: TestCronConfig_Parse — parses crons map
ADD: TestServiceConfig_BackupFields — parses backup_storage, backup_cron
```

### internal/api/config/validate_test.go

```
UPDATE: tests referencing old ingress fields
ADD: TestValidate_CloudflareFirewallRequiresCFIngress — firewall: cloudflare without cloudflare-managed = error
ADD: TestValidate_CertWithoutKey — cert without key = error
```

### internal/api/plan/plan_test.go

```
UPDATE: TestPlan_EdgeOverlayCarriesProviderAndProxyToIngressAndDNS — use new ingress config
UPDATE: TestPlan_IngressConfigProvidedTLSResolvedFromEnv — use new cert/key fields
ADD: TestPlan_CronSteps — crons in config produce cron.set steps
ADD: TestPlan_CronRemoved — cron in reality but not desired produces cron.delete
ADD: TestPlan_CloudflareManaged_IngressParams — cloudflare-managed produces correct step params
```

### internal/api/plan/resolve_test.go

```
UPDATE: step sequence assertions if cron phases change ordering
ADD: TestResolve_ManagedCronsStripped — managed-owned crons excluded from Build()
```

### internal/api/handlers/config_test.go

```
UPDATE: TestConfig_ManagedPlanIncludesExpandedServices — adjust for new schema fields if any
UPDATE: any tests using old ingress config format
```

### internal/api/handlers/deploy_test.go

```
UPDATE: any tests using old ingress config format
```

### internal/api/handlers/executor_test.go

```
NO CHANGES — executor dispatches steps, doesn't know about config schema
```

---

## Existing tests to DELETE

### internal/core/ CLI tests

All existing CLI-level tests in `internal/core/` that test cobra command flag parsing move to `pkg/commands/*_test.go`. If `internal/core/` had any (currently only `resolve_test.go`), they stay — resolve logic stays in `internal/core/`.

### internal/cli/ CLI tests

`internal/cli/` currently has no test files. The new `backend_test.go` is the first.

---

## Tests that DON'T change

### pkg/core/*_test.go — all unchanged

Business logic tests. The Backend calls these functions. The functions don't change. Tests don't change.

- `pkg/core/compute_test.go`
- `pkg/core/service_test.go`
- `pkg/core/secret_test.go`
- `pkg/core/storage_test.go`
- `pkg/core/cron_test.go`
- `pkg/core/dns_test.go`
- `pkg/core/ingress_test.go`
- `pkg/core/tls_test.go`
- `pkg/core/describe_test.go`
- `pkg/core/managed_list_test.go`
- `pkg/core/build_test.go`
- `pkg/core/wait_test.go`
- `pkg/core/cluster_test.go`
- `pkg/core/output_test.go`

### pkg/managed/*_test.go — all unchanged

Compiler tests. Don't depend on CLI or API.

### pkg/kube/*_test.go — all unchanged

Manifest generation tests. Don't depend on CLI or API.

### pkg/provider/*_test.go — all unchanged

Provider tests. Don't depend on CLI or API.

### internal/api/handlers/ — mostly unchanged

API handler tests stay. They test HTTP endpoints, not CLI commands. Only update tests that reference old ingress schema fields.

### internal/render/render_test.go — unchanged

Renderer tests. Don't depend on CLI structure.

---

## Summary

| Layer | New tests | Updated tests | Deleted tests |
|-------|-----------|---------------|---------------|
| pkg/commands/ | ~60 (flag parsing + mock backend) | 0 | 0 |
| internal/core/ | ~7 (DirectBackend) | 0 | 0 (resolve_test stays) |
| internal/cli/ | ~13 (CloudBackend) | 0 | 0 |
| internal/api/config/ | ~7 | ~3 | 0 |
| internal/api/plan/ | ~4 | ~3 | 0 |
| internal/api/handlers/ | 0 | ~2 | 0 |
| pkg/core/ | 0 | 0 | 0 |
| pkg/managed/ | 0 | 0 | 0 |
| **Total** | **~91** | **~8** | **0** |
