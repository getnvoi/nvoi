package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Client is the typed Kubernetes client used by every nvoi operation that
// touches the cluster. It owns:
//
//   - a kubernetes.Interface for typed CRUD on standard resources,
//   - a *rest.Config wired to dial the apiserver over an SSH tunnel.
//
// New() establishes the tunnel; Close() tears it down. The client is safe to
// share across goroutines — client-go itself is concurrency-safe.
type Client struct {
	cs       kubernetes.Interface
	cfg      *rest.Config
	cleanup  func()
	closeMu  sync.Mutex
	closed   bool
	apiHost  string // apiserver address as it appears in kubeconfig (ip:port), used for ServerName
	tunnel   string // local tunnel addr (host:port) actually dialed by transport
	masterIP string // apiserver private IP (informational, used for diagnostics)

	// ExecFunc, when non-nil, replaces the default SPDY exec implementation.
	// Tests set this to capture stdin / canned-respond without a real
	// apiserver. Production leaves it nil.
	ExecFunc func(ctx context.Context, req ExecRequest) error
}

// NewForTest builds a Client around a typed clientset (typically the
// client-go fake). The resulting client has no SSH tunnel and no rest.Config
// — Close() is a no-op. Exec() returns an error unless ExecFunc is set.
func NewForTest(cs kubernetes.Interface) *Client {
	return &Client{cs: cs}
}

// Clientset returns the typed kubernetes clientset.
func (c *Client) Clientset() kubernetes.Interface { return c.cs }

// RESTConfig returns the underlying REST config, useful for sub-clients
// such as remotecommand for exec.
func (c *Client) RESTConfig() *rest.Config { return c.cfg }

// Close tears down the tunnel. Safe to call multiple times.
func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.cleanup != nil {
		c.cleanup()
	}
	return nil
}

// New builds a Client by:
//  1. SFTP-fetching the deploy-user kubeconfig from the master,
//  2. Opening an SSH-tunneled TCP listener on a free localhost port,
//  3. Rewriting kubeconfig.server to point at the tunnel,
//  4. Setting TLSClientConfig.ServerName to the original apiserver host so
//     cert validation still works against the SAN list,
//  5. Building the typed clientset.
//
// Caller must call Close() when done with the client.
func New(ctx context.Context, ssh utils.SSHClient) (*Client, error) {
	raw, err := fetchKubeconfig(ctx, ssh)
	if err != nil {
		return nil, fmt.Errorf("fetch kubeconfig: %w", err)
	}

	apiHost, err := apiserverHost(raw)
	if err != nil {
		return nil, err
	}

	// Open SSH-tunneled local listener to the apiserver.
	tunnel, cleanup, err := openTunnel(ssh, apiHost)
	if err != nil {
		return nil, fmt.Errorf("open kube tunnel to %s: %w", apiHost, err)
	}

	cfg, err := buildRESTConfig(raw, tunnel, apiHost)
	if err != nil {
		cleanup()
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("kubernetes clientset: %w", err)
	}

	c := &Client{
		cs:       cs,
		cfg:      cfg,
		cleanup:  cleanup,
		apiHost:  apiHost,
		tunnel:   tunnel,
		masterIP: hostOnly(apiHost),
	}
	return c, nil
}

// fetchKubeconfig reads /etc/rancher/k3s/k3s.yaml from the master via SFTP.
// The k3s install pipeline rewrites this file to use the master's private IP
// for the apiserver address (see infra/k3s.go:setupKubeconfig).
func fetchKubeconfig(ctx context.Context, ssh utils.SSHClient) ([]byte, error) {
	// We use the deploy user's copy because that's what was rewritten with the
	// private IP and chmod'd readable.
	path := fmt.Sprintf("/home/%s/.kube/config", utils.DefaultUser)
	out, err := ssh.Run(ctx, "cat "+path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("kubeconfig at %s is empty", path)
	}
	return out, nil
}

// apiserverHost returns the host:port of the apiserver as kubeconfig declares it.
// This is what TLS server name validation must match against the cert SANs.
func apiserverHost(raw []byte) (string, error) {
	cfg, err := clientcmd.Load(raw)
	if err != nil {
		return "", fmt.Errorf("parse kubeconfig: %w", err)
	}
	clusterName, err := currentCluster(cfg)
	if err != nil {
		return "", err
	}
	cluster := cfg.Clusters[clusterName]
	if cluster == nil {
		return "", fmt.Errorf("cluster %q not found in kubeconfig", clusterName)
	}
	u, err := url.Parse(cluster.Server)
	if err != nil {
		return "", fmt.Errorf("parse cluster server URL: %w", err)
	}
	host := u.Host
	if host == "" {
		return "", fmt.Errorf("cluster server URL %q missing host", cluster.Server)
	}
	if !strings.Contains(host, ":") {
		host = host + ":6443"
	}
	return host, nil
}

func currentCluster(cfg *clientcmdapi.Config) (string, error) {
	ctxName := cfg.CurrentContext
	if ctxName == "" {
		// fallback: pick the only context if there's exactly one
		if len(cfg.Contexts) == 1 {
			for k := range cfg.Contexts {
				ctxName = k
			}
		}
	}
	if ctxName == "" {
		return "", fmt.Errorf("kubeconfig has no current-context")
	}
	kctx := cfg.Contexts[ctxName]
	if kctx == nil {
		return "", fmt.Errorf("context %q missing", ctxName)
	}
	return kctx.Cluster, nil
}

// buildRESTConfig produces a *rest.Config that:
//   - dials the local tunnel address,
//   - validates TLS using the CA embedded in the fetched kubeconfig, with
//     ServerName pinned to the apiserver's real host so cert SANs match.
//
// We always parse the raw bytes we fetched from the master. Going through
// clientcmd's deferred-loading path would silently pick up the operator's
// local ~/.kube/config and validate the apiserver cert against the wrong
// CA, producing "x509: certificate signed by unknown authority".
func buildRESTConfig(raw []byte, tunnelAddr, apiHost string) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(raw)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	overrides := &clientcmd.ConfigOverrides{
		ClusterInfo: clientcmdapi.Cluster{
			Server:        "https://" + tunnelAddr,
			TLSServerName: hostOnly(apiHost),
		},
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	cfg.Timeout = 30 * time.Second
	return cfg, nil
}

// openTunnel opens a localhost listener that forwards every accepted
// connection through ssh.DialTCP to the remote apiserver address.
//
// Lifecycle:
//   - the listener and the accept goroutine live until cleanup() is called,
//   - each accepted local connection spawns one goroutine per direction; both
//     end when either side closes the connection.
//
// Errors from DialTCP are swallowed deliberately — client-go's transport
// retries on connection failures, and we don't want every transient SSH
// reconnect to surface as a CLI error. If something is really wrong, the
// next API call returns a typed "connection refused" error.
func openTunnel(ssh utils.SSHClient, remoteAddr string) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().String()

	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go pipe(ssh, local, remoteAddr)
		}
	}()

	cleanup := func() { _ = ln.Close() }
	return addr, cleanup, nil
}

func pipe(ssh utils.SSHClient, local net.Conn, remoteAddr string) {
	defer local.Close()
	remote, err := ssh.DialTCP(context.Background(), remoteAddr)
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(remote, local)
		_ = remote.Close()
		close(done)
	}()
	_, _ = io.Copy(local, remote)
	<-done
}

// hostOnly strips the port off an addr like "10.0.1.5:6443" → "10.0.1.5".
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
