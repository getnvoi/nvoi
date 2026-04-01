# Build Checklist — Minimally Viable

Each phase ends with a real smoke test against live infrastructure.
Every command is `set`. `compute set` provisions + installs k3s (master by default, `--worker` to join).
`bin/deploy` runs end to end, always same outcome.

---

## Phase 1 — Compute + SSH ✅
**Goal:** `nvoi compute set master` provisions a Hetzner server with k3s, Docker, registry.

- [x] `core/naming.go` — deterministic naming from app+env
- [x] `core/httpclient.go` — JSON-over-HTTP client for provider APIs
- [x] `core/poll.go` — retry with deadline
- [x] `core/sshutil.go` — SSH key derivation
- [x] `core/ssh.go` — SSHClient interface
- [x] `infra/ssh.go` — SSH client + TOFU known hosts
- [x] `infra/server.go` — WaitSSH, EnsureDocker over SSH
- [x] `infra/cloudinit.go` — cloud-init rendering
- [x] `infra/k3s.go` — InstallK3sMaster, JoinK3sWorker, EnsureRegistry
- [x] `provider/compute.go` — ComputeProvider interface + credential schema
- [x] `provider/resolve.go` — schema-based credential resolution
- [x] `provider/hetzner/` — full Hetzner implementation (server, firewall, network)
- [x] `app/compute.go` — ComputeSet, ComputeDelete, ComputeList
- [x] `app/resources.go` — Resources (list all under account)
- [x] `app/ssh.go` — SSH command execution on master
- [x] `cmd/compute.go` — thin cobra wrapper
- [x] `cmd/resources.go` — thin cobra wrapper
- [x] `cmd/ssh.go` — thin cobra wrapper
- [x] `cmd/resolve.go` — centralized env/flag resolution
- [x] `cmd/table.go` — table rendering

- [x] **Smoke test**
  ```bash
  bin/cli compute set master --provider hetzner --type cax11 --region fsn1
  bin/cli compute set worker-1 --provider hetzner --type cax21 --region fsn1 --worker
  bin/cli ssh --provider hetzner "kubectl get nodes"
  bin/cli resources --provider hetzner
  bin/cli compute delete worker-1 --provider hetzner -y
  bin/cli compute delete master --provider hetzner -y
  ```

---

## Phase 2 — Service + Apply + Show
**Goal:** `nvoi apply` deploys a container and `nvoi show` prints live state.

- [ ] **Implement service set/delete**
  - [ ] `app/service.go` + `cmd/service.go`

- [ ] **Port k8s generation + kubectl**
  - [ ] `kube/generate.go` — service flags → Deployment/StatefulSet/Service YAML
  - [ ] `kube/apply.go` — kubectl apply/delete over SSH
  - [ ] `kube/rollout.go` — kubectl rollout status over SSH

- [ ] **Implement apply**
  - [ ] `app/apply.go` + `cmd/apply.go`

- [ ] **Implement show**
  - [ ] `app/show.go` + `cmd/show.go`

- [ ] **Implement operations**
  - [ ] `app/logs.go` + `cmd/logs.go`
  - [ ] `app/exec.go` + `cmd/exec.go`

- [ ] **Smoke test**
  ```bash
  bin/cli compute set master --provider hetzner --type cax11 --region fsn1
  bin/cli service set web --image nginx --port 80
  bin/cli apply --provider hetzner
  bin/cli show --provider hetzner
  bin/cli logs web --provider hetzner
  bin/cli ssh --provider hetzner "curl -s localhost:80"
  ```

---

## Phase 3 — Volume + DNS + Secrets
**Goal:** Full deploy with persistent storage, custom domain, HTTPS, k8s secrets.

- [ ] **Implement volume commands**
  - [ ] `app/volume.go` + `cmd/volume.go`

- [ ] **Port Cloudflare DNS**
  - [ ] `provider/cloudflare/dns.go`

- [ ] **Implement DNS commands**
  - [ ] `app/dns.go` + `cmd/dns.go`

- [ ] **Port Caddy into apply**
  - [ ] `kube/caddy.go`

- [ ] **Implement secret commands**
  - [ ] `app/secret.go` + `cmd/secret.go`

- [ ] **Smoke test**
  ```bash
  bin/cli compute set master --provider hetzner --type cax11 --region fsn1
  bin/cli volume set pgdata --size 20 --provider hetzner
  bin/cli service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data
  bin/cli service set web --image nginx --port 80
  bin/cli secret set API_KEY mykey123
  bin/cli dns set web final.nvoi.to --provider cloudflare --zone nvoi.to
  bin/cli apply --provider hetzner
  bin/cli show --provider hetzner
  curl https://final.nvoi.to
  ```

---

## Phase 4 — Storage + Destroy + Polish
**Goal:** Object storage, full teardown, idempotency.

- [ ] **Implement storage commands**
  - [ ] `app/storage.go` + `cmd/storage.go`

- [ ] **Implement destroy**
  - [ ] `app/destroy.go` + `cmd/destroy.go`

- [ ] **Smoke test — full lifecycle (`bin/deploy` + `bin/destroy`)**

---

## Phase 5 — Build ✅

- [x] `app/build.go` — BuildRun, BuildList, BuildLatest
- [x] `cmd/build.go` — `nvoi build`, `nvoi build list`, `nvoi build latest`
- [x] `provider/local/builder.go` — local docker buildx + SSH tunnel + permission fix
- [x] `provider/daytona/` — Daytona SDK sandbox builder (DinD, clone, tunnel, push)
- [x] Git auth: signed URL → `gh auth token` → `--git-token` → `GITHUB_TOKEN`
- [x] DinD support in Docker Compose (`docker-data` volume for cache persistence)

- [x] **Smoke test**
  ```bash
  bin/cli build --provider hetzner --builder daytona --source benbonnet/dummy-rails --name web
  bin/cli build --provider hetzner --builder local --source /path/to/app --name web
  bin/cli build list --provider hetzner
  bin/cli build latest web --provider hetzner
  ```

---

## Future

- [ ] Workers: `nvoi compute set worker-N --provider ... --worker`
- [ ] Hooks: pre_deploy / post_build
- [ ] API server consuming `internal/app/` directly
