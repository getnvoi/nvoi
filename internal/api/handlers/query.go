package handlers

import (
	"net/http"
	"strconv"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListInstances returns all servers for a repo's compute provider.
//
// @Summary     List instances
// @Description Returns all servers from the compute provider. Same data as `nvoi instance list`.
// @Tags        instances
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  object "Servers from compute provider"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/instances [get]
func ListInstances(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		servers, err := pkgcore.ComputeList(c.Request.Context(), pkgcore.ComputeListRequest{Cluster: *cluster})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, servers)
	}
}

// ListVolumes returns all volumes for a repo's compute provider.
//
// @Summary     List volumes
// @Description Returns all volumes from the compute provider. Same data as `nvoi volume list`.
// @Tags        volumes
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  object "Volumes from compute provider"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/volumes [get]
func ListVolumes(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		volumes, err := pkgcore.VolumeList(c.Request.Context(), pkgcore.VolumeListRequest{Cluster: *cluster})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, volumes)
	}
}

// ListDNSRecords returns all DNS A records for a repo's DNS provider.
//
// @Summary     List DNS records
// @Description Returns all A records from the DNS provider. Same data as `nvoi dns list`.
// @Tags        dns
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  object "DNS A records"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/dns [get]
func ListDNSRecords(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		rc, env, err := latestConfigAndEnv(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		creds, err := resolveAllCredentials(rc, env)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if rc.DNSProvider == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no dns_provider configured"})
			return
		}

		records, err := pkgcore.DNSList(c.Request.Context(), pkgcore.DNSListRequest{
			DNS: pkgcore.ProviderRef{Name: string(rc.DNSProvider), Creds: creds.DNS},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, records)
	}
}

// ListSecrets returns all secret key names in the cluster.
//
// @Summary     List secrets
// @Description Returns all secret key names from the cluster. Same data as `nvoi secret list`.
// @Tags        secrets
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  string "Secret key names"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/secrets [get]
func ListSecrets(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		keys, err := pkgcore.SecretList(c.Request.Context(), pkgcore.SecretListRequest{Cluster: *cluster})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, keys)
	}
}

// ListStorageBuckets returns all storage buckets configured in the cluster.
//
// @Summary     List storage
// @Description Returns all storage buckets discovered from k8s secrets. Same data as `nvoi storage list`.
// @Tags        storage
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  object "Storage items (name + bucket)"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/storage [get]
func ListStorageBuckets(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		items, err := pkgcore.StorageList(c.Request.Context(), pkgcore.StorageListRequest{Cluster: *cluster})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, items)
	}
}

// EmptyStorage deletes all objects in a storage bucket.
//
// @Summary     Empty storage bucket
// @Description Deletes all objects in the named storage bucket. Same as `nvoi storage empty <name>`.
// @Tags        storage
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Param       name         path     string true "Storage name"
// @Success     200          {object} statusResponse
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/storage/{name}/empty [post]
func EmptyStorage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		name := c.Param("name")

		rc, env, err := latestConfigAndEnv(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		creds, err := resolveAllCredentials(rc, env)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		err = pkgcore.StorageEmpty(c.Request.Context(), pkgcore.StorageEmptyRequest{
			Cluster: *cluster,
			Storage: pkgcore.ProviderRef{Name: string(rc.StorageProvider), Creds: creds.Storage},
			Name:    name,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "emptied"})
	}
}

// ListBuilds returns all images in the cluster registry.
//
// @Summary     List builds
// @Description Returns all images and their tags from the cluster registry. Same data as `nvoi build list`.
// @Tags        builds
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  object "Registry images with tags"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/builds [get]
func ListBuilds(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		images, err := pkgcore.BuildList(c.Request.Context(), pkgcore.BuildListRequest{Cluster: *cluster})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, images)
	}
}

// BuildLatestImage returns the latest image ref for a build name.
//
// @Summary     Get latest build image
// @Description Returns the latest image reference for a build name. Same as `nvoi build latest <name>`.
// @Tags        builds
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Param       name         path     string true "Build name"
// @Success     200          {object} buildLatestResponse
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/builds/{name}/latest [get]
func BuildLatestImage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		name := c.Param("name")

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ref, err := pkgcore.BuildLatest(c.Request.Context(), pkgcore.BuildLatestRequest{
			Cluster: *cluster,
			Name:    name,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"image": ref})
	}
}

// PruneBuild deletes old image tags, keeping the most recent N.
//
// @Summary     Prune build images
// @Description Deletes old image tags for a build name, keeping the most recent N. Same as `nvoi build prune <name> --keep N`.
// @Tags        builds
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string            true "Workspace ID" format(uuid)
// @Param       repo_id      path     string            true "Repo ID"      format(uuid)
// @Param       name         path     string            true "Build name"
// @Param       body         body     buildPruneRequest true "Prune options"
// @Success     200          {object} statusResponse
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/builds/{name}/prune [post]
func PruneBuild(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		name := c.Param("name")

		var req buildPruneRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		err = pkgcore.BuildPrune(c.Request.Context(), pkgcore.BuildPruneRequest{
			Cluster: *cluster,
			Name:    name,
			Keep:    req.Keep,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "pruned"})
	}
}

// ServiceLogs streams pod logs for a service.
//
// @Summary     Stream service logs
// @Description Streams pod logs for a service as plain text. Same as `nvoi logs <service>`.
// @Tags        services
// @Produce     text/plain
// @Security    BearerAuth
// @Param       workspace_id path     string true  "Workspace ID" format(uuid)
// @Param       repo_id      path     string true  "Repo ID"      format(uuid)
// @Param       service      path     string true  "Service name"
// @Param       follow       query    bool   false "Follow log output"
// @Param       tail         query    int    false "Number of lines from the end"
// @Param       since        query    string false "Show logs since duration (e.g. 5m, 1h)"
// @Param       previous     query    bool   false "Show previous container logs"
// @Param       timestamps   query    bool   false "Include timestamps"
// @Success     200          {string} string "Log stream"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/services/{service}/logs [get]
func ServiceLogs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		service := c.Param("service")

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		tail, _ := strconv.Atoi(c.DefaultQuery("tail", "50"))

		c.Header("Content-Type", "text/plain")
		c.Writer.WriteHeader(http.StatusOK)

		cluster.Output = &streamOutput{w: c.Writer}

		logsErr := pkgcore.Logs(c.Request.Context(), pkgcore.LogsRequest{
			Cluster:    *cluster,
			Service:    service,
			Follow:     c.DefaultQuery("follow", "") == "true",
			Tail:       tail,
			Since:      c.DefaultQuery("since", ""),
			Previous:   c.DefaultQuery("previous", "") == "true",
			Timestamps: c.DefaultQuery("timestamps", "") == "true",
		})
		if logsErr != nil {
			c.Writer.Write([]byte("\nerror: " + logsErr.Error() + "\n"))
		}
	}
}

// ExecCommand runs a command in a service pod and streams output.
//
// @Summary     Exec in service pod
// @Description Runs a command in the first pod of a service and streams output. Same as `nvoi exec <service> -- <command>`.
// @Tags        services
// @Accept      json
// @Produce     text/plain
// @Security    BearerAuth
// @Param       workspace_id path     string        true "Workspace ID" format(uuid)
// @Param       repo_id      path     string        true "Repo ID"      format(uuid)
// @Param       service      path     string        true "Service name"
// @Param       body         body     execRequest   true "Command to run"
// @Success     200          {string} string        "Command output stream"
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/services/{service}/exec [post]
func ExecCommand(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		service := c.Param("service")

		var req execRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.Header("Content-Type", "text/plain")
		c.Writer.WriteHeader(http.StatusOK)

		cluster.Output = &streamOutput{w: c.Writer}

		execErr := pkgcore.Exec(c.Request.Context(), pkgcore.ExecRequest{
			Cluster: *cluster,
			Service: service,
			Command: req.Command,
		})
		if execErr != nil {
			c.Writer.Write([]byte("\nerror: " + execErr.Error() + "\n"))
		}
	}
}
