# Deploy a Rails app

```bash
# 1. Provision
nvoi compute set master --provider hetzner --type cax11 --region fsn1
nvoi compute set worker-1 --provider hetzner --type cax21 --region fsn1 --worker
nvoi volume set pgdata --size 20 --provider hetzner

# 2. Services
nvoi service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data --env POSTGRES_PASSWORD=secret
nvoi service set web --build myorg/myapp --port 3000 --replicas 2 --health-path /up --env DATABASE_URL=postgres://db:5432/myapp

# 3. Secrets
nvoi secret set RAILS_MASTER_KEY "$RAILS_MASTER_KEY"

# 4. DNS
nvoi dns set web app.nvoi.to --provider cloudflare --zone nvoi.to

# 5. Deploy
nvoi apply --provider hetzner

# 6. Check
nvoi show --provider hetzner

# 7. Operate
nvoi logs web --provider hetzner -f
nvoi exec web --provider hetzner -- rails console
nvoi ssh --provider hetzner "df -h"

# 8. Tear down
bin/destroy
```

Run it all at once — idempotent, self-healing:
```bash
NVOI_ENV=production bin/deploy
```
