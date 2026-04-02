package daytona

import (
	"context"
	"fmt"
	"time"

	apiclient "github.com/daytonaio/daytona/libs/api-client-go"
	daytonasdk "github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/options"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

const (
	snapshotName  = "nvoi-dind"
	snapshotImage = "docker:28.3.3-dind"
)

// sandbox abstracts a Daytona sandbox for testing.
type sandbox interface {
	Upload(ctx context.Context, data []byte, destination string) error
	Clone(ctx context.Context, url, path, branch, username, password string) error
	Exec(ctx context.Context, command string, timeout time.Duration) (string, int, error)
	CreateSession(ctx context.Context, sessionID string) error
	ExecSessionAsync(ctx context.Context, sessionID, command string) (string, error)
	StreamSessionLogs(ctx context.Context, sessionID, commandID string, stdout, stderr chan<- string) error
	SessionCommand(ctx context.Context, sessionID, commandID string) (int, bool, error)
	DeleteSession(ctx context.Context, sessionID string) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	State() string
}

// client abstracts Daytona SDK client operations for testing.
type client interface {
	FindOrStartOrCreate(ctx context.Context, name string) (sandbox, error)
	EnsureSnapshot(ctx context.Context) error
}

// sdkClient wraps the Daytona SDK client.
type sdkClient struct {
	client *daytonasdk.Client
}

func newSDKClient(apiKey string) (client, error) {
	c, err := daytonasdk.NewClientWithConfig(&types.DaytonaConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, err
	}
	return &sdkClient{client: c}, nil
}

func (c *sdkClient) FindOrStartOrCreate(ctx context.Context, name string) (sandbox, error) {
	if err := c.EnsureSnapshot(ctx); err != nil {
		return nil, err
	}

	sb, err := c.client.Get(ctx, name)
	if err == nil {
		wrapped := &sdkSandbox{sb: sb}
		switch sb.State {
		case apiclient.SANDBOXSTATE_STARTED:
			return wrapped, nil
		case apiclient.SANDBOXSTATE_STOPPED, apiclient.SANDBOXSTATE_ARCHIVED:
			if err := wrapped.Start(ctx); err != nil {
				return nil, err
			}
			return wrapped, nil
		default:
			if err := wrapped.Start(ctx); err == nil {
				return wrapped, nil
			}
			// Fall through to create when existing sandbox cannot resume
		}
	}

	created, err := c.client.Create(ctx, types.SnapshotParams{
		SandboxBaseParams: types.SandboxBaseParams{
			Name: name,
		},
		Snapshot: snapshotName,
	}, options.WithTimeout(2*time.Minute))
	if err != nil {
		return nil, err
	}
	return &sdkSandbox{sb: created}, nil
}

func (c *sdkClient) EnsureSnapshot(ctx context.Context) error {
	snapshot, err := c.client.Snapshot.Get(ctx, snapshotName)
	if err == nil {
		return waitForSnapshotActive(ctx, c.client, snapshot.Name)
	}

	_, logChan, err := c.client.Snapshot.Create(ctx, &types.CreateSnapshotParams{
		Name:       snapshotName,
		Image:      snapshotImage,
		Entrypoint: []string{"dockerd-entrypoint.sh"},
		Resources: &types.Resources{
			CPU:    2,
			Memory: 4,
			Disk:   8,
		},
	})
	if err != nil {
		return err
	}
	// Drain log channel
	if logChan != nil {
		go func() {
			for range logChan {
			}
		}()
	}
	return waitForSnapshotActive(ctx, c.client, snapshotName)
}

func waitForSnapshotActive(ctx context.Context, client *daytonasdk.Client, name string) error {
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for snapshot %s to become active", name)
		case <-ticker.C:
			snapshot, err := client.Snapshot.Get(ctx, name)
			if err != nil {
				return err
			}
			if snapshot.State == "active" {
				return nil
			}
		}
	}
}

// sdkSandbox wraps a Daytona SDK sandbox.
type sdkSandbox struct {
	sb *daytonasdk.Sandbox
}

func (s *sdkSandbox) Upload(ctx context.Context, data []byte, destination string) error {
	return s.sb.FileSystem.UploadFile(ctx, data, destination)
}

func (s *sdkSandbox) Clone(ctx context.Context, url, path, branch, username, password string) error {
	opts := []func(*options.GitClone){}
	if branch != "" {
		opts = append(opts, options.WithBranch(branch))
	}
	if username != "" {
		opts = append(opts, options.WithUsername(username))
	}
	if password != "" {
		opts = append(opts, options.WithPassword(password))
	}
	return s.sb.Git.Clone(ctx, url, path, opts...)
}

func (s *sdkSandbox) Exec(ctx context.Context, command string, timeout time.Duration) (string, int, error) {
	opts := []func(*options.ExecuteCommand){}
	if timeout > 0 {
		opts = append(opts, options.WithExecuteTimeout(timeout))
	}
	result, err := s.sb.Process.ExecuteCommand(ctx, command, opts...)
	if err != nil {
		return "", 0, err
	}
	return result.Result, result.ExitCode, nil
}

func (s *sdkSandbox) CreateSession(ctx context.Context, sessionID string) error {
	return s.sb.Process.CreateSession(ctx, sessionID)
}

func (s *sdkSandbox) ExecSessionAsync(ctx context.Context, sessionID, command string) (string, error) {
	result, err := s.sb.Process.ExecuteSessionCommand(ctx, sessionID, command, true, false)
	if err != nil {
		return "", err
	}
	cmdID, _ := result["id"].(string)
	return cmdID, nil
}

func (s *sdkSandbox) StreamSessionLogs(ctx context.Context, sessionID, commandID string, stdout, stderr chan<- string) error {
	return s.sb.Process.GetSessionCommandLogsStream(ctx, sessionID, commandID, stdout, stderr)
}

func (s *sdkSandbox) SessionCommand(ctx context.Context, sessionID, commandID string) (int, bool, error) {
	result, err := s.sb.Process.GetSessionCommand(ctx, sessionID, commandID)
	if err != nil {
		return 0, false, err
	}
	exitCode, ok := result["exitCode"]
	if !ok {
		return 0, false, nil
	}
	switch v := exitCode.(type) {
	case int:
		return v, true, nil
	case int32:
		return int(v), true, nil
	case int64:
		return int(v), true, nil
	case float64:
		return int(v), true, nil
	default:
		return 0, true, nil
	}
}

func (s *sdkSandbox) DeleteSession(ctx context.Context, sessionID string) error {
	return s.sb.Process.DeleteSession(ctx, sessionID)
}

func (s *sdkSandbox) Start(ctx context.Context) error {
	return s.sb.Start(ctx)
}

func (s *sdkSandbox) Stop(ctx context.Context) error {
	return s.sb.Stop(ctx)
}

func (s *sdkSandbox) State() string {
	return string(s.sb.State)
}
