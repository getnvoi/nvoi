// Package infra provides low-level infrastructure primitives.
// All remote operations go through SSH. No agent daemon.
package infra

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SSHClient wraps a persistent SSH connection.
type SSHClient struct {
	conn *ssh.Client
	addr string
	user string
}

// ConnectSSH establishes an SSH connection using private key data.
func ConnectSSH(ctx context.Context, addr, user string, privateKey []byte) (*SSHClient, error) {
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: tofuHostKeyCallback(addr),
		Timeout:         10 * time.Second,
	}

	var conn *ssh.Client
	done := make(chan struct{})
	var dialErr error
	go func() {
		conn, dialErr = ssh.Dial("tcp", addr, config)
		close(done)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		if dialErr != nil {
			if isAuthFailure(dialErr) {
				return nil, fmt.Errorf("ssh dial %s: %w", addr, ErrAuthFailed)
			}
			return nil, fmt.Errorf("ssh dial %s: %w", addr, dialErr)
		}
	}

	return &SSHClient{conn: conn, addr: addr, user: user}, nil
}

// Run executes a command and returns its combined output.
// Respects context cancellation — kills the remote process if ctx is done.
func (c *SSHClient) Run(ctx context.Context, cmd string) ([]byte, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(cmd)
		done <- result{out, err}
	}()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			detail := strings.TrimSpace(string(r.out))
			if detail != "" {
				return r.out, fmt.Errorf("run %q: %s: %w", cmd, detail, r.err)
			}
			return r.out, fmt.Errorf("run %q: %w", cmd, r.err)
		}
		return r.out, nil
	}
}

// RunStream executes a command and streams stdout/stderr to the provided writers.
func (c *SSHClient) RunStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	sess.Stdout = stdout
	sess.Stderr = stderr

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("run %q: %w", cmd, err)
		}
		return nil
	}
}

// RunWithStdin executes cmd on the remote host with stdin piped from the
// provided reader, streaming stdout/stderr back to the provided writers.
// The SSH BuildProvider uses this to feed the push-side registry password
// into `docker login --password-stdin` on the builder — no secrets on argv,
// no temp files.
func (c *SSHClient) RunWithStdin(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stderr

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("run %q: %w", cmd, err)
		}
		return nil
	}
}

// Upload writes data to a remote file via SFTP.
func (c *SSHClient) Upload(_ context.Context, local io.Reader, remotePath string, mode fs.FileMode) error {
	sftpClient, err := sftp.NewClient(c.conn)
	if err != nil {
		return fmt.Errorf("sftp client: %w", err)
	}
	defer sftpClient.Close()

	f, err := sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open remote %s: %w", remotePath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, local); err != nil {
		return fmt.Errorf("write remote %s: %w", remotePath, err)
	}

	if err := sftpClient.Chmod(remotePath, mode); err != nil {
		return fmt.Errorf("chmod remote %s: %w", remotePath, err)
	}

	return nil
}

// Stat returns file info for a remote path via SFTP.
func (c *SSHClient) Stat(_ context.Context, remotePath string) (*utils.RemoteFileInfo, error) {
	sftpClient, err := sftp.NewClient(c.conn)
	if err != nil {
		return nil, fmt.Errorf("sftp client: %w", err)
	}
	defer sftpClient.Close()

	fi, err := sftpClient.Stat(remotePath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", remotePath, err)
	}

	return &utils.RemoteFileInfo{
		Path:  remotePath,
		Size:  fi.Size(),
		Mode:  fi.Mode(),
		IsDir: fi.IsDir(),
	}, nil
}

// DialTCP opens an SSH channel-forwarded connection to a remote TCP address.
func (c *SSHClient) DialTCP(_ context.Context, remoteAddr string) (net.Conn, error) {
	conn, err := c.conn.Dial("tcp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", remoteAddr, err)
	}
	return conn, nil
}

// Close shuts down the SSH connection.
func (c *SSHClient) Close() error {
	return c.conn.Close()
}

var _ utils.SSHClient = (*SSHClient)(nil)

// LocalForward opens a local TCP listener that tunnels through SSH to a remote address.
// Returns the local address (e.g. "localhost:54321") and a cleanup function.
func LocalForward(client utils.SSHClient, remoteAddr string) (localAddr string, cleanup func(), err error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().String()

	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer local.Close()
				remote, err := client.DialTCP(context.Background(), remoteAddr)
				if err != nil {
					return
				}
				defer remote.Close()
				done := make(chan struct{})
				go func() { io.Copy(remote, local); close(done) }()
				io.Copy(local, remote)
				<-done
			}()
		}
	}()

	return addr, func() { ln.Close() }, nil
}

// --- Sentinel errors ---

// ErrHostKeyChanged is returned when a known host presents a different key.
var ErrHostKeyChanged = fmt.Errorf("ssh host key changed")

// ErrNoKnownHost is returned by ClearKnownHost when the host isn't in
// the known_hosts file. Callers use errors.Is — never string matching.
var ErrNoKnownHost = fmt.Errorf("no known host entry")

// ErrAuthFailed is returned when SSH authentication fails.
var ErrAuthFailed = fmt.Errorf("ssh authentication failed")

func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "unable to authenticate") {
		return true
	}
	if strings.Contains(msg, "no supported methods remain") {
		return true
	}
	if strings.Contains(msg, "disconnect") && strings.Contains(msg, "handshake failed") {
		return true
	}
	return false
}

// --- TOFU known hosts ---

var (
	knownHostsMu sync.Mutex
	knownHosts   map[string]string
	khLoaded     bool
)

func knownHostsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nvoi", "known_hosts")
}

func loadKnownHosts() {
	if khLoaded {
		return
	}
	knownHosts = make(map[string]string)
	data, err := os.ReadFile(knownHostsPath())
	if err != nil {
		khLoaded = true
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			knownHosts[parts[0]] = parts[1]
		}
	}
	khLoaded = true
}

func saveKnownHosts() error {
	path := knownHostsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var b strings.Builder
	for host, key := range knownHosts {
		fmt.Fprintf(&b, "%s %s\n", host, key)
	}
	return os.WriteFile(path, []byte(b.String()), 0600)
}

// ClearKnownHost removes a host entry from the known_hosts file.
func ClearKnownHost(host string) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	loadKnownHosts()
	if _, exists := knownHosts[host]; !exists {
		if _, exists := knownHosts[host+":22"]; exists {
			host = host + ":22"
		} else {
			return fmt.Errorf("%w for %s", ErrNoKnownHost, host)
		}
	}
	delete(knownHosts, host)
	return saveKnownHosts()
}

// ClearAllKnownHosts removes all entries.
func ClearAllKnownHosts() error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	knownHosts = make(map[string]string)
	khLoaded = true
	return saveKnownHosts()
}

// ListKnownHosts returns all stored host entries.
func ListKnownHosts() map[string]string {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	loadKnownHosts()
	result := make(map[string]string, len(knownHosts))
	for k, v := range knownHosts {
		result[k] = v
	}
	return result
}

func tofuHostKeyCallback(addr string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()
		loadKnownHosts()

		host := addr
		if !strings.Contains(host, ":") {
			host = hostname
		}

		encoded := base64.StdEncoding.EncodeToString(key.Marshal())
		stored, exists := knownHosts[host]
		if !exists {
			knownHosts[host] = encoded
			_ = saveKnownHosts()
			return nil
		}
		if stored != encoded {
			return fmt.Errorf("%w for %s — server was likely recreated.\nRun: nvoi known-hosts clear %s\nOr remove the entry from %s", ErrHostKeyChanged, host, host, knownHostsPath())
		}
		return nil
	}
}
