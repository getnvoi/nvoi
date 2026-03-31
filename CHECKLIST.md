# Build Checklist — Minimally Viable

Each phase ends with a real smoke test against live infrastructure.
Every command is `set` — idempotent, self-healing. `bin/deploy` runs end to end, always same outcome.

---

## Phase 1 — Compute + SSH
**Goal:** `nvoi compute set master` provisions a Hetzner server you can SSH into.

- [ ] **Port core utilities**
  - [ ] `core/httpclient.go` — HTTP client wrapper
  - [ ] `core/poll.go` — retry with deadline
  - [ ] `core/sshutil.go` — key derivation
  - [ ] `core/naming.go` — already written, verify tests

- [ ] **Port SSH**
  - [ ] `infra/ssh.go` — already ported, verify it compiles

- [ ] **Port Hetzner provider**
  - [ ] `provider/hetzner/client.go` — New, ValidateCredentials, ArchForType
  - [ ] `provider/hetzner/firewall.go` — full firewall CRUD
  - [ ] `provider/hetzner/network.go` — full network CRUD
  - [ ] `provider/hetzner/server.go` — EnsureServer, GetServerByName, DeleteServer, ListServers
  - [ ] `provider/hetzner/volume.go` — full volume CRUD
  - [ ] Register in `provider/resolve.go`

- [ ] **Port server provisioning**
  - [ ] `infra/server.go` — EnsureDocker over SSH, cloud-init rendering

- [ ] **Implement commands**
  - [ ] `cmd/compute.go` set: resolve provider → firewall → network → server → SSH → docker → write .env
  - [ ] `cmd/compute.go` list: query provider API by label, print table
  - [ ] `cmd/compute.go` delete: provider API teardown
  - [ ] `cmd/ssh.go`: resolve master from provider API, SSH in, run command

- [ ] **Smoke test**
  ```bash
  bin/cli compute set master --provider hetzner --type cax11 --region fsn1
  bin/cli ssh "uname -a"
  bin/cli compute list
  bin/cli compute delete master
  ```

---

## Phase 2 — Bootstrap + Service + Apply + Show
**Goal:** `nvoi apply` deploys a container and `nvoi show` prints live state.

- [ ] **Port k3s bootstrap**
  - [ ] `infra/k3s.go` — install k3s, join workers, registry, all over SSH

- [ ] **Implement bootstrap**
  - [ ] `cmd/bootstrap.go`: resolve master from provider API → SSH → install k3s → registry

- [ ] **Implement service set/delete**
  - [ ] `cmd/service.go` set: resolve master → generate k8s YAML from flags → kubectl apply over SSH
  - [ ] `cmd/service.go` delete: kubectl delete over SSH

- [ ] **Port k8s generation + kubectl**
  - [ ] `kube/generate.go` — service flags → Deployment/StatefulSet/Service YAML
  - [ ] `kube/apply.go` — kubectl apply/delete over SSH
  - [ ] `kube/rollout.go` — kubectl rollout status over SSH (use -o json)

- [ ] **Implement apply**
  - [ ] `cmd/apply.go`: resolve master → query cluster → rebuild if needed → kubectl apply → rollout

- [ ] **Implement show**
  - [ ] `cmd/show.go`: provider API for servers/volumes + SSH kubectl for pods/services → print everything

- [ ] **Implement operations**
  - [ ] `cmd/logs.go` — kubectl logs over SSH
  - [ ] `cmd/exec.go` — kubectl exec over SSH

- [ ] **Smoke test**
  ```bash
  bin/cli compute set master --provider hetzner --type cax11 --region fsn1
  bin/cli bootstrap
  bin/cli service set web --image nginx --port 80
  bin/cli apply
  bin/cli show
  bin/cli logs web
  bin/cli ssh "curl -s localhost:80"
  ```

---

## Phase 3 — Volume + DNS + Secrets
**Goal:** Full deploy with persistent storage, custom domain, HTTPS, k8s secrets.

- [ ] **Port volume mounting**
  - [ ] `infra/volume.go` — attach, resolve device, mount, fstab — all over SSH

- [ ] **Implement volume commands**
  - [ ] `cmd/volume.go` set: provider API + SSH mount
  - [ ] `cmd/volume.go` delete: provider API detach
  - [ ] `cmd/volume.go` list: provider API query

- [ ] **Port Cloudflare DNS**
  - [ ] `provider/cloudflare/dns.go` — EnsureARecord, DeleteARecord, ListARecords
  - [ ] Register in `provider/resolve.go`

- [ ] **Implement DNS commands**
  - [ ] `cmd/dns.go` set/delete/list: DNS API directly

- [ ] **Port Caddy into apply**
  - [ ] `kube/caddy.go` — Caddyfile + Caddy Deployment generation
  - [ ] TLS verification: SSH check cert + HTTP probe

- [ ] **Implement secret commands**
  - [ ] `cmd/secret.go` set/delete/list: kubectl over SSH

- [ ] **Smoke test**
  ```bash
  bin/cli compute set master --provider hetzner --type cax11 --region fsn1
  bin/cli bootstrap
  bin/cli volume set pgdata --size 20
  bin/cli service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data --env POSTGRES_PASSWORD=secret
  bin/cli service set web --image nginx --port 80
  bin/cli secret set API_KEY mykey123
  bin/cli dns set web final.nvoi.to --provider cloudflare --zone nvoi.to
  bin/cli apply
  bin/cli show
  curl https://final.nvoi.to
  ```

---

## Phase 4 — Storage + Destroy + Polish
**Goal:** Object storage, full teardown, idempotency.

- [ ] **Implement storage commands**
  - [ ] `cmd/storage.go` set: bucket API + write credentials to .env
  - [ ] `cmd/storage.go` delete: remove from .env

- [ ] **Implement destroy**
  - [ ] `cmd/destroy.go`: query all infra by label → tear down in reverse → clear .env

- [ ] **Polish**
  - [ ] All commands idempotent — `bin/deploy` runs end to end, always same outcome
  - [ ] Error messages actionable
  - [ ] Shell escaping for all SSH commands

- [ ] **Smoke test — full lifecycle (`bin/deploy`)**
  ```bash
  bin/deploy   # runs everything — idempotent, self-healing
  bin/cli show
  bin/cli destroy --yes
  bin/cli compute list  # should be empty
  ```

---

## Phase 5 — Build (future)

- [ ] Port Daytona builder
- [ ] `cmd/build.go`: resolve builder → build repo → push to registry → update service image

---

## Future

- [ ] Workers: `nvoi compute set worker-1 ...` + k3s worker join
- [ ] Hooks: pre_deploy / post_build
- [ ] API server on top of same commands
