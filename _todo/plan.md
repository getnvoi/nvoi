Full blueprint: shared command tree, two backends                                                                                                                                                           
                                                                                                                                                                                                              
  ---                                                                                                                                                                                                         
  Architecture
                                                                                                                                                                                                              
  pkg/commands/          — shared command definitions (cobra commands + flag parsing)
  pkg/commands/backend.go — Backend interface                                                                                                                                                                 
  pkg/commands/instance.go — instance set/delete commands
  pkg/commands/firewall.go — firewall set                                                                                                                                                                     
  pkg/commands/volume.go — volume set/delete                              
  pkg/commands/storage.go — storage set/delete                                                                                                                                                                
  pkg/commands/service.go — service set/delete                            
  pkg/commands/database.go — database set/delete/list/backup
  pkg/commands/agent.go — agent set/delete/list/exec/logs                                                                                                                                                     
  pkg/commands/cron.go — cron set/delete                                                                                                                                                                      
  pkg/commands/secret.go — secret set/delete/list/reveal                                                                                                                                                      
  pkg/commands/dns.go — dns set/delete                                                                                                                                                                        
  pkg/commands/ingress.go — ingress set/delete                            
  pkg/commands/build.go — build                                                                                                                                                                               
  pkg/commands/describe.go — describe                                     
  pkg/commands/logs.go — logs
  pkg/commands/exec.go — exec                                                                                                                                                                                 
  pkg/commands/ssh.go — ssh
  pkg/commands/resources.go — resources                                                                                                                                                                       
                                                                          
  internal/core/          — direct backend implementation + root wiring                                                                                                                                       
  internal/core/backend.go — DirectBackend implements Backend
  internal/core/root.go — wires pkg/commands with DirectBackend                                                                                                                                               
                                                                          
  internal/cli/           — cloud backend implementation + root wiring                                                                                                                                        
  internal/cli/backend.go — CloudBackend implements Backend               
  internal/cli/root.go — wires pkg/commands with CloudBackend
                                                                                                                                                                                                              
  ---
  pkg/commands/backend.go — the interface                                                                                                                                                                     
                                                                          
  package commands

  import "context"                                                                                                                                                                                            
   
  type Backend interface {                                                                                                                                                                                    
      // Infrastructure                                                   
      InstanceSet(ctx context.Context, name, serverType, region string, worker bool) error
      InstanceDelete(ctx context.Context, name string) error
      FirewallSet(ctx context.Context, preset string, rules map[string][]string) error                                                                                                                        
      VolumeSet(ctx context.Context, name string, size int, server string) error
      VolumeDelete(ctx context.Context, name string) error                                                                                                                                                    
                                                                          
      // Storage                                                                                                                                                                                              
      StorageSet(ctx context.Context, name string, cors bool, expireDays int) error
      StorageDelete(ctx context.Context, name string) error                                                                                                                                                   
      StorageEmpty(ctx context.Context, name string) error
                                                                                                                                                                                                              
      // Application                                                      
      ServiceSet(ctx context.Context, name string, opts ServiceOpts) error
      ServiceDelete(ctx context.Context, name string) error                                                                                                                                                   
      CronSet(ctx context.Context, name string, opts CronOpts) error
      CronDelete(ctx context.Context, name string) error                                                                                                                                                      
                                                                                                                                                                                                              
      // Managed
      DatabaseSet(ctx context.Context, name string, opts DatabaseOpts) error                                                                                                                                  
      DatabaseDelete(ctx context.Context, name, kind string) error        
      DatabaseList(ctx context.Context) ([]ManagedServiceInfo, error)                                                                                                                                         
      BackupCreate(ctx context.Context, name, kind string) error
      BackupList(ctx context.Context, name string, backupStorage string) ([]BackupArtifactInfo, error)                                                                                                        
      BackupDownload(ctx context.Context, name, backupStorage, key string) ([]byte, error)                                                                                                                    
      AgentSet(ctx context.Context, name string, opts AgentOpts) error                                                                                                                                        
      AgentDelete(ctx context.Context, name, kind string) error                                                                                                                                               
      AgentList(ctx context.Context) ([]ManagedServiceInfo, error)                                                                                                                                            
      AgentExec(ctx context.Context, name, kind string, command []string) error                                                                                                                               
      AgentLogs(ctx context.Context, name, kind string, opts LogsOpts) error
                                                                                                                                                                                                              
      // Secrets                                                          
      SecretSet(ctx context.Context, key, value string) error                                                                                                                                                 
      SecretDelete(ctx context.Context, key string) error                                                                                                                                                     
      SecretList(ctx context.Context) ([]string, error)
      SecretReveal(ctx context.Context, key string) (string, error)                                                                                                                                           
                                                                                                                                                                                                              
      // Build
      Build(ctx context.Context, targets map[string]string) error                                                                                                                                             
      BuildList(ctx context.Context) error                                
      BuildLatest(ctx context.Context, name string) (string, error)
      BuildPrune(ctx context.Context, name string, keep int) error                                                                                                                                            
                                                                                                                                                                                                              
      // DNS + Ingress                                                                                                                                                                                        
      DNSSet(ctx context.Context, routes []RouteArg, cloudflareManaged bool) error                                                                                                                            
      DNSDelete(ctx context.Context, routes []RouteArg) error                                                                                                                                                 
      IngressSet(ctx context.Context, route RouteArg, cloudflareManaged bool, certPEM, keyPEM string) error
      IngressDelete(ctx context.Context, route RouteArg, cloudflareManaged bool) error                                                                                                                        
                                                                                                                                                                                                              
      // Operational                                                                                                                                                                                          
      Describe(ctx context.Context) error                                                                                                                                                                     
      Resources(ctx context.Context) error                                
      Logs(ctx context.Context, service string, opts LogsOpts) error
      Exec(ctx context.Context, service string, command []string) error                                                                                                                                       
      SSH(ctx context.Context, command string) error
                                                                                                                                                                                                              
      // Cloud-only                                                       
      Deploy(ctx context.Context) error                                                                                                                                                                       
  }                                                                       

  type ServiceOpts struct {
      Image      string
      Build      string                                                                                                                                                                                       
      Port       int
      Replicas   int                                                                                                                                                                                          
      Command    string                                                   
      Server     string
      Health     string
      Env        []string
      Secrets    []string                                                                                                                                                                                     
      Storage    []string
      Volumes    []string                                                                                                                                                                                     
  }                                                                       

  type CronOpts struct {
      Image    string
      Schedule string
      Command  string
      Server   string
      Env      []string
      Secrets  []string                                                                                                                                                                                       
      Storage  []string
  }                                                                                                                                                                                                           
                                                                          
  type DatabaseOpts struct {
      Kind          string
      Image         string
      Secrets       []string
      BackupStorage string                                                                                                                                                                                    
      BackupCron    string
  }                                                                                                                                                                                                           
                                                                          
  type AgentOpts struct {
      Kind    string
      Secrets []string
  }

  type LogsOpts struct {                                                                                                                                                                                      
      Follow     bool
      Tail       int                                                                                                                                                                                          
      Since      string                                                   
      Previous   bool
      Timestamps bool
  }

  type RouteArg struct {                                                                                                                                                                                      
      Service string
      Domains []string                                                                                                                                                                                        
  }                                                                       

  type ManagedServiceInfo struct {
      Name        string
      ManagedKind string
      Category    string
      Image       string
      Ready       string
      Children    []string                                                                                                                                                                                    
  }
                                                                                                                                                                                                              
  type BackupArtifactInfo struct {                                        
      Key          string
      Size         int64
      LastModified string                                                                                                                                                                                     
  }
                                                                                                                                                                                                              
  ---                                                                     
  pkg/commands/*.go — shared command definitions
                                                
  Each file defines one cobra command group. Example for instance:
                                                                                                                                                                                                              
  // pkg/commands/instance.go
  package commands                                                                                                                                                                                            
                                                                          
  import "github.com/spf13/cobra"                                                                                                                                                                             
                                                                          
  func NewInstanceCmd(b Backend) *cobra.Command {                                                                                                                                                             
      cmd := &cobra.Command{
          Use:   "instance",                                                                                                                                                                                  
          Short: "Manage servers",                                        
      }
      cmd.AddCommand(newInstanceSetCmd(b))                                                                                                                                                                    
      cmd.AddCommand(newInstanceDeleteCmd(b))
      return cmd                                                                                                                                                                                              
  }                                                                       

  func newInstanceSetCmd(b Backend) *cobra.Command {
      cmd := &cobra.Command{
          Use:  "set [name]",                                                                                                                                                                                 
          Short: "Create or update a server",
          Args: cobra.ExactArgs(1),                                                                                                                                                                           
          RunE: func(cmd *cobra.Command, args []string) error {                                                                                                                                               
              serverType, _ := cmd.Flags().GetString("compute-type")
              region, _ := cmd.Flags().GetString("compute-region")                                                                                                                                            
              worker, _ := cmd.Flags().GetBool("worker")                                                                                                                                                      
              return b.InstanceSet(cmd.Context(), args[0], serverType, region, worker)
          },                                                                                                                                                                                                  
      }                                                                   
      cmd.Flags().String("compute-type", "", "server type (required)")
      cmd.Flags().String("compute-region", "", "server region (required)")                                                                                                                                    
      cmd.Flags().Bool("worker", false, "join as worker node")
      return cmd                                                                                                                                                                                              
  }                                                                       
                                                                                                                                                                                                              
  Same pattern for every command. Flags defined once. b.XXX() called once. No duplication.                                                                                                                    
   
  The database and agent commands include both config-mutation commands (set/delete) and operational commands (list/backup/exec/logs). All go through the Backend — direct backend executes immediately, cloud
   backend calls the API.                                                 
                                                                                                                                                                                                              
  ---                                                                     
  internal/core/backend.go — direct backend

  package core
                                                                                                                                                                                                              
  type DirectBackend struct {
      cluster  app.Cluster                                                                                                                                                                                    
      dns      app.ProviderRef                                            
      storage  app.ProviderRef                                                                                                                                                                                
      builder  string
      creds    map[string]string                                                                                                                                                                              
  }                                                                                                                                                                                                           
   
  func (d *DirectBackend) InstanceSet(ctx context.Context, name, serverType, region string, worker bool) error {                                                                                              
      _, err := app.ComputeSet(ctx, app.ComputeSetRequest{                
          Cluster:    d.cluster,                                                                                                                                                                              
          Name:       name,
          ServerType: serverType,                                                                                                                                                                             
          Region:     region,                                             
          Worker:     worker,                                                                                                                                                                                 
      })
      return err                                                                                                                                                                                              
  }                                                                       

  func (d *DirectBackend) ServiceSet(ctx context.Context, name string, opts commands.ServiceOpts) error {
      return app.ServiceSet(ctx, app.ServiceSetRequest{
          Cluster:    d.cluster,                                                                                                                                                                              
          Name:       name,
          Image:      opts.Image,                                                                                                                                                                             
          Port:       opts.Port,                                          
          // ... map all fields                                                                                                                                                                               
      })
  }                                                                                                                                                                                                           
                                                                          
  // ... every Backend method maps to a pkg/core function call                                                                                                                                                
   
  Constructed from resolved flags (app name, env, provider, credentials, SSH key) — same as today's resolveCluster.                                                                                           
                                                                          
  ---                                                                                                                                                                                                         
  internal/cli/backend.go — cloud backend                                 
                                                                                                                                                                                                              
  package cli
                                                                                                                                                                                                              
  type CloudBackend struct {                                              
      client *APIClient
      wsID   string
      repoID string
  }

  func (c *CloudBackend) InstanceSet(ctx context.Context, name, serverType, region string, worker bool) error {                                                                                               
      cfg, env, err := c.loadConfig()
      if err != nil {                                                                                                                                                                                         
          return err                                                      
      }
      if cfg.Servers == nil {
          cfg.Servers = map[string]config.Server{}                                                                                                                                                            
      }
      cfg.Servers[name] = config.Server{Type: serverType, Region: region}                                                                                                                                     
      return c.pushConfig(cfg, env)                                                                                                                                                                           
  }
                                                                                                                                                                                                              
  func (c *CloudBackend) ServiceSet(ctx context.Context, name string, opts commands.ServiceOpts) error {                                                                                                      
      cfg, env, err := c.loadConfig()
      if err != nil {                                                                                                                                                                                         
          return err                                                      
      }
      cfg.Services[name] = config.Service{
          Image:    opts.Image,
          Port:     opts.Port,                                                                                                                                                                                
          // ... map all fields
      }                                                                                                                                                                                                       
      return c.pushConfig(cfg, env)                                       
  }

  func (c *CloudBackend) Deploy(ctx context.Context) error {
      // POST /deploy + poll logs
      return c.triggerDeploy()                                                                                                                                                                                
  }                                                                                                                                                                                                           
                                                                                                                                                                                                              
  // Operational commands call the API endpoints directly:                                                                                                                                                    
  func (c *CloudBackend) DatabaseList(ctx context.Context) ([]commands.ManagedServiceInfo, error) {
      var services []commands.ManagedServiceInfo                                                                                                                                                              
      return services, c.client.Do("GET", c.repoPath("/database"), nil, &services)
  }                                                                                                                                                                                                           
                                                                          
  func (c *CloudBackend) Describe(ctx context.Context) error {                                                                                                                                                
      // GET /describe, render                                            
  }                                                                                                                                                                                                           
                                                                          
  // ... every Backend method either mutates config+push or calls an API endpoint                                                                                                                             
   
  loadConfig / pushConfig are the two shared helpers — load latest from API, push updated version.                                                                                                            
                                                                          
  ---                                                                                                                                                                                                         
  internal/core/root.go — simplified                                      
                                                                                                                                                                                                              
  func Root() *cobra.Command {
      root := &cobra.Command{Use: "nvoi", Short: "Deploy containers to cloud servers"}                                                                                                                        
                                                                                                                                                                                                              
      // Persistent flags for provider resolution
      root.PersistentFlags().String("app-name", "", "...")                                                                                                                                                    
      root.PersistentFlags().String("environment", "", "...")                                                                                                                                                 
      // ... compute-provider, etc.
                                                                                                                                                                                                              
      // Lazy backend construction — resolved on first command execution  
      var backend commands.Backend                                                                                                                                                                            
      resolveBackend := func(cmd *cobra.Command) commands.Backend {                                                                                                                                           
          if backend == nil {
              backend = buildDirectBackend(cmd) // resolves cluster, providers, SSH                                                                                                                           
          }                                                                                                                                                                                                   
          return backend
      }                                                                                                                                                                                                       
                                                                          
      // All commands come from pkg/commands, wired with lazy direct backend                                                                                                                                  
      root.AddCommand(commands.NewInstanceCmd(lazyBackend(resolveBackend)))
      root.AddCommand(commands.NewServiceCmd(lazyBackend(resolveBackend)))                                                                                                                                    
      root.AddCommand(commands.NewDatabaseCmd(lazyBackend(resolveBackend)))
      root.AddCommand(commands.NewAgentCmd(lazyBackend(resolveBackend)))                                                                                                                                      
      // ... all others                                                                                                                                                                                       
   
      return root                                                                                                                                                                                             
  }                                                                       

  ---
  internal/cli/root.go — simplified

  func Root() *cobra.Command {
      root := &cobra.Command{Use: "nvoi", Short: "Deploy containers to cloud servers"}                                                                                                                        
  
      // Cloud-specific: login, workspaces, repos                                                                                                                                                             
      root.AddCommand(newLoginCmd())                                      
      root.AddCommand(newWhoamiCmd())                                                                                                                                                                         
      root.AddCommand(newWorkspacesCmd())                                                                                                                                                                     
      root.AddCommand(newReposCmd())
                                                                                                                                                                                                              
      // Lazy backend — authenticated API client                                                                                                                                                              
      var backend commands.Backend
      resolveBackend := func(cmd *cobra.Command) commands.Backend {                                                                                                                                           
          if backend == nil {                                             
              backend = buildCloudBackend() // loads auth, resolves workspace+repo
          }                                                                                                                                                                                                   
          return backend
      }                                                                                                                                                                                                       
                                                                          
      // Same commands as direct CLI, different backend                                                                                                                                                       
      root.AddCommand(commands.NewInstanceCmd(lazyBackend(resolveBackend)))
      root.AddCommand(commands.NewServiceCmd(lazyBackend(resolveBackend)))                                                                                                                                    
      root.AddCommand(commands.NewDatabaseCmd(lazyBackend(resolveBackend)))                                                                                                                                   
      root.AddCommand(commands.NewAgentCmd(lazyBackend(resolveBackend)))
      // ... all others                                                                                                                                                                                       
                                                                                                                                                                                                              
      // Cloud-only
      root.AddCommand(commands.NewDeployCmd(lazyBackend(resolveBackend)))                                                                                                                                     
                                                                          
      return root
  }

  ---                                                                                                                                                                                                         
  internal/api/config/schema.go — ingress cleanup
                                                                                                                                                                                                              
  Replace IngressConfig + IngressTLSConfig + IngressEdgeConfig with:      
                                                                                                                                                                                                              
  type IngressConfig struct {
      CloudflareManaged bool   `json:"cloudflare-managed,omitempty" yaml:"cloudflare-managed,omitempty"`                                                                                                      
      Cert              string `json:"cert,omitempty" yaml:"cert,omitempty"`                                                                                                                                  
      Key               string `json:"key,omitempty" yaml:"key,omitempty"`
  }                                                                                                                                                                                                           
                                                                          
  Add to Config:                                                                                                                                                                                              
  type Config struct {
      // existing...                                                                                                                                                                                          
      Crons map[string]Cron `json:"crons,omitempty" yaml:"crons,omitempty"`
  }
                                                                                                                                                                                                              
  Add to Service:
  type Service struct {                                                                                                                                                                                       
      // existing...                                                      
      BackupStorage string `json:"backup_storage,omitempty" yaml:"backup_storage,omitempty"`
      BackupCron    string `json:"backup_cron,omitempty" yaml:"backup_cron,omitempty"`
  }                                                                                                                                                                                                           
   
  ---                                                                                                                                                                                                         
  internal/api/plan/plan.go — update                                      
                                                                                                                                                                                                              
  - ingressRouteParams derives exposure/tls/edge from CloudflareManaged bool
  - Delete desiredIngressExposure, desiredIngressTLSMode                                                                                                                                                      
  - Add setCrons / diffCrons phases                                       
                                                                                                                                                                                                              
  ---                                                                     
  internal/api/plan/resolve.go — update                                                                                                                                                                       
                                                                          
  Strip managed-owned crons from cfg.Crons before Build().
                                                                                                                                                                                                              
  ---
  Examples rewrite                                                                                                                                                                                            
                                                                          
  Delete:
  - examples/cloud/hetzner/config.yaml
  - examples/cloud/scaleway/config.yaml                                                                                                                                                                       
  - examples/cloud/aws/config.yaml     
  - examples/cloud/empty.yaml                                                                                                                                                                                 
                                                                          
  Rewrite examples/cloud/*/deploy and examples/cloud/*/destroy as imperative scripts. Mirror examples/core/* with bin/cloud instead of bin/core, bin/cloud deploy at the end:
                                                                                                                                                                                                              
  examples/cloud/hetzner/deploy
  #!/bin/bash                                                                                                                                                                                                 
  set -euo pipefail                                                       
  set -a; source examples/.env; set +a
                                      
  bin/cloud login                                                                                                                                                                                             
  bin/cloud repos use dummy-rails
                                                                                                                                                                                                              
  bin/cloud instance set master --compute-type cx23 --compute-region fsn1 
  bin/cloud instance set worker-1 --compute-type cx33 --compute-region fsn1 --worker
  bin/cloud firewall set cloudflare                                                 
                                                                                                                                                                                                              
  bin/cloud secret set RAILS_MASTER_KEY "$RAILS_MASTER_KEY"
  bin/cloud secret set POSTGRES_PASSWORD "$POSTGRES_PASSWORD"                                                                                                                                                 
  bin/cloud secret set POSTGRES_USER "$POSTGRES_USER"                     
  bin/cloud secret set POSTGRES_DB "$POSTGRES_DB"                                                                                                                                                             
                                                 
  bin/cloud storage set assets --cors                                                                                                                                                                         
  bin/cloud storage set db-backups --expire-days 30                       
                                                   
  bin/cloud database set db --type postgres \                                                                                                                                                                 
    --secret POSTGRES_PASSWORD \             
    --secret POSTGRES_USER \                                                                                                                                                                                  
    --secret POSTGRES_DB \                                                
    --backup-storage db-backups \
    --backup-cron "0 2 * * *"    
                             
  bin/cloud build --target web:benbonnet/dummy-rails                                                                                                                                                          
                                                    
  bin/cloud service set web \                                                                                                                                                                                 
    --build web --port 80 --replicas 2 --health /up --server worker-1 \   
    --env RAILS_ENV=production --env POSTGRES_HOST=db \                
    --secret POSTGRES_PASSWORD --secret POSTGRES_USER --secret POSTGRES_DB \                                                                                                                                  
    --secret RAILS_MASTER_KEY --storage assets                                                                                                                                                                
                                                                                                                                                                                                              
  bin/cloud service set jobs \                                                                                                                                                                                
    --build web --command "bin/jobs" --server worker-1 \                                                                                                                                                      
    --env RAILS_ENV=production --env POSTGRES_HOST=db \ 
    --secret POSTGRES_PASSWORD --secret POSTGRES_USER --secret POSTGRES_DB \                                                                                                                                  
    --secret RAILS_MASTER_KEY                                               
                             
  bin/cloud ingress set web:hz-cloud.nvoi.to --cloudflare-managed                                                                                                                                             
  bin/cloud dns set web:hz-cloud.nvoi.to --cloudflare-managed    
                                                                                                                                                                                                              
  bin/cloud deploy                                                        
                  
  examples/cloud/hetzner/destroy
  #!/bin/bash                                                                                                                                                                                                 
  set -euo pipefail
  set -a; source examples/.env; set +a                                                                                                                                                                        
                                                                          
  bin/cloud login
  bin/cloud repos use dummy-rails
                                 
  bin/cloud ingress delete web:hz-cloud.nvoi.to --cloudflare-managed
  bin/cloud dns delete web:hz-cloud.nvoi.to                                                                                                                                                                   
  bin/cloud service delete jobs            
  bin/cloud service delete web                                                                                                                                                                                
  bin/cloud database delete db --type postgres                            
  bin/cloud storage delete db-backups         
  bin/cloud storage delete assets                                                                                                                                                                             
  bin/cloud secret delete RAILS_MASTER_KEY
  bin/cloud secret delete POSTGRES_PASSWORD                                                                                                                                                                   
  bin/cloud secret delete POSTGRES_USER                                   
  bin/cloud secret delete POSTGRES_DB  
  bin/cloud instance delete worker-1 
  bin/cloud instance delete master  
                                                                                                                                                                                                              
  bin/cloud deploy
                                                                                                                                                                                                              
  Same pattern for scaleway (no --cloudflare-managed) and aws (storage-provider aws).
                                                                                                                                                                                                              
  ---
  Files deleted                                                                                                                                                                                               
                                                                                                                                                                                                              
  - internal/core/instance.go — replaced by pkg/commands/instance.go + internal/core/backend.go
  - internal/core/service.go — same                                                                                                                                                                           
  - internal/core/volume.go — same                                        
  - internal/core/dns.go (CLI) — same                                                                                                                                                                         
  - internal/core/ingress.go (CLI) — same                                                                                                                                                                     
  - internal/core/storage.go (CLI) — same
  - internal/core/secret.go (CLI) — same                                                                                                                                                                      
  - internal/core/build.go (CLI) — same                                                                                                                                                                       
  - internal/core/describe.go (CLI) — same
  - internal/core/logs.go (CLI) — same                                                                                                                                                                        
  - internal/core/exec.go (CLI) — same                                    
  - internal/core/ssh.go (CLI) — same                                                                                                                                                                         
  - internal/core/cron.go (CLI) — same
  - internal/core/database.go — replaced by pkg/commands/database.go + backend                                                                                                                                
  - internal/core/agent_cmd.go — replaced by pkg/commands/agent.go + backend                                                                                                                                  
  - internal/core/managed.go — resolveCluster moves to internal/core/backend.go, execOperation moves there too, shared helpers (verifyManagedKind, verifyStorageExists, deleteByShape, ensureBackupImage) move
   to direct backend                                                                                                                                                                                          
  - internal/cli/database.go — replaced by shared commands + cloud backend
  - internal/cli/agent.go — replaced                                                                                                                                                                          
  - internal/cli/describe.go — replaced                                   
  - internal/cli/resources.go — replaced                                                                                                                                                                      
  - examples/cloud/*/config.yaml — deleted                                                                                                                                                                    
  - examples/cloud/empty.yaml — deleted
                                                                                                                                                                                                              
  Files created                                                                                                                                                                                               
   
  - pkg/commands/backend.go — Backend interface + option types                                                                                                                                                
  - pkg/commands/instance.go                                              
  - pkg/commands/firewall.go                                                                                                                                                                                  
  - pkg/commands/volume.go
  - pkg/commands/storage.go                                                                                                                                                                                   
  - pkg/commands/service.go                                               
  - pkg/commands/database.go
  - pkg/commands/agent.go
  - pkg/commands/cron.go                                                                                                                                                                                      
  - pkg/commands/secret.go
  - pkg/commands/dns.go                                                                                                                                                                                       
  - pkg/commands/ingress.go                                               
  - pkg/commands/build.go
  - pkg/commands/describe.go
  - pkg/commands/logs.go                                                                                                                                                                                      
  - pkg/commands/exec.go
  - pkg/commands/ssh.go                                                                                                                                                                                       
  - pkg/commands/resources.go                                             
  - pkg/commands/deploy.go — cloud-only deploy command
  - internal/core/backend.go — DirectBackend                                                                                                                                                                  
  - internal/cli/backend.go — CloudBackend                                                                                                                                                                    
                                                                                                                                                                                                              
  Files modified                                                                                                                                                                                              
                                                                          
  - internal/core/root.go — simplified, wires shared commands with DirectBackend                                                                                                                              
  - internal/core/resolve.go — stays, used by DirectBackend construction
  - internal/cli/root.go — simplified, wires shared commands with CloudBackend                                                                                                                                
  - internal/cli/client.go — stays, used by CloudBackend                  
  - internal/api/config/schema.go — IngressConfig cleanup, Crons map, Service backup fields                                                                                                                   
  - internal/api/config/validate.go — updated validation                  
  - internal/api/plan/plan.go — ingress param derivation, cron phases                                                                                                                                         
  - internal/api/plan/resolve.go — strip managed crons                                                                                                                                                        
  - internal/api/plan/plan_test.go — updated for new schema                                                                                                                                                   
                                                                                                                                                                                                              
  No changes to                                                                                                                                                                                               
   
  - pkg/core/ — all business logic unchanged                                                                                                                                                                  
  - pkg/managed/ — compiler unchanged                                     
  - pkg/kube/ — manifest generation unchanged
  - pkg/provider/ — providers unchanged
  - pkg/infra/ — SSH/bootstrap unchanged                                                                                                                                                                      
  - internal/api/handlers/ — API handlers unchanged
  - internal/api/handlers/executor.go — executor unchanged                                                                                                                                                    
  - internal/render/ — renderers unchanged                                
                                                                                                                                                                                                              
  ---                                                                     
  Summary
         
  One command tree (pkg/commands/). Two backends (DirectBackend, CloudBackend). Same commands, same flags, same help text. Direct executes immediately. Cloud mutates config + deploy at the end. Zero
  duplication. Cloud examples become identical to direct examples with bin/cloud + deploy.                                                                                                                    
   
