package testutil

import (
	"context"
	"io"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// MockCompute implements provider.ComputeProvider for testing.
type MockCompute struct {
	Servers                  []*provider.Server
	Volumes                  []*provider.Volume
	EnsureServerFn           func(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error)
	DeleteServerFn           func(ctx context.Context, req provider.DeleteServerRequest) error
	EnsureVolumeFn           func(ctx context.Context, req provider.CreateVolumeRequest) (*provider.Volume, error)
	ReconcileFirewallRulesFn func(ctx context.Context, name string, allowed provider.PortAllowList) error
	GetFirewallRulesFn       func(ctx context.Context, name string) (provider.PortAllowList, error)
}

func (m *MockCompute) ValidateCredentials(ctx context.Context) error { return nil }
func (m *MockCompute) ArchForType(instanceType string) string        { return "amd64" }

func (m *MockCompute) EnsureServer(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error) {
	if m.EnsureServerFn != nil {
		return m.EnsureServerFn(ctx, req)
	}
	return m.Servers[0], nil
}

func (m *MockCompute) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	if m.DeleteServerFn != nil {
		return m.DeleteServerFn(ctx, req)
	}
	return nil
}

func (m *MockCompute) ListServers(ctx context.Context, labels map[string]string) ([]*provider.Server, error) {
	if labels == nil {
		return m.Servers, nil
	}
	var filtered []*provider.Server
	for _, s := range m.Servers {
		filtered = append(filtered, s)
	}
	return filtered, nil
}

func (m *MockCompute) ListAllFirewalls(ctx context.Context) ([]*provider.Firewall, error) {
	return nil, nil
}
func (m *MockCompute) ListAllNetworks(ctx context.Context) ([]*provider.Network, error) {
	return nil, nil
}

func (m *MockCompute) EnsureVolume(ctx context.Context, req provider.CreateVolumeRequest) (*provider.Volume, error) {
	if m.EnsureVolumeFn != nil {
		return m.EnsureVolumeFn(ctx, req)
	}
	if len(m.Volumes) > 0 {
		return m.Volumes[0], nil
	}
	return &provider.Volume{Name: req.Name, Size: req.Size}, nil
}

func (m *MockCompute) ResizeVolume(ctx context.Context, id string, sizeGB int) error { return nil }
func (m *MockCompute) DetachVolume(ctx context.Context, name string) error           { return nil }
func (m *MockCompute) DeleteVolume(ctx context.Context, name string) error           { return nil }

func (m *MockCompute) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	return m.Volumes, nil
}

func (m *MockCompute) GetPrivateIP(ctx context.Context, serverID string) (string, error) {
	for _, s := range m.Servers {
		if s.ID == serverID {
			return s.PrivateIP, nil
		}
	}
	return "", nil
}

func (m *MockCompute) ResolveDevicePath(vol *provider.Volume) string {
	return vol.DevicePath
}

func (m *MockCompute) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

func (m *MockCompute) ReconcileFirewallRules(ctx context.Context, name string, allowed provider.PortAllowList) error {
	if m.ReconcileFirewallRulesFn != nil {
		return m.ReconcileFirewallRulesFn(ctx, name, allowed)
	}
	return nil
}

func (m *MockCompute) GetFirewallRules(ctx context.Context, name string) (provider.PortAllowList, error) {
	if m.GetFirewallRulesFn != nil {
		return m.GetFirewallRulesFn(ctx, name)
	}
	return nil, nil
}

var _ provider.ComputeProvider = (*MockCompute)(nil)

// MockDNS implements provider.DNSProvider for testing.
type MockDNS struct {
	Records  []provider.DNSRecord
	EnsuredA []string // domains passed to EnsureARecord
	DeletedA []string // domains passed to DeleteARecord
}

func (m *MockDNS) ValidateCredentials(ctx context.Context) error { return nil }

func (m *MockDNS) EnsureARecord(ctx context.Context, domain, ip string, proxied bool) error {
	m.EnsuredA = append(m.EnsuredA, domain)
	return nil
}

func (m *MockDNS) DeleteARecord(ctx context.Context, domain string) error {
	m.DeletedA = append(m.DeletedA, domain)
	return nil
}

func (m *MockDNS) ListARecords(ctx context.Context) ([]provider.DNSRecord, error) {
	return m.Records, nil
}

func (m *MockDNS) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

var _ provider.DNSProvider = (*MockDNS)(nil)

// MockBucket implements provider.BucketProvider for testing.
type MockBucket struct {
	Buckets     []string
	CredsResult provider.BucketCredentials
}

func (m *MockBucket) ValidateCredentials(ctx context.Context) error { return nil }

func (m *MockBucket) EnsureBucket(ctx context.Context, name string) error {
	m.Buckets = append(m.Buckets, name)
	return nil
}

func (m *MockBucket) EmptyBucket(ctx context.Context, name string) error  { return nil }
func (m *MockBucket) DeleteBucket(ctx context.Context, name string) error { return nil }

func (m *MockBucket) SetCORS(ctx context.Context, name string, origins, methods []string) error {
	return nil
}
func (m *MockBucket) ClearCORS(ctx context.Context, name string) error { return nil }
func (m *MockBucket) SetLifecycle(ctx context.Context, name string, expireDays int) error {
	return nil
}

func (m *MockBucket) Credentials(ctx context.Context) (provider.BucketCredentials, error) {
	return m.CredsResult, nil
}

func (m *MockBucket) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

var _ provider.BucketProvider = (*MockBucket)(nil)

// MockOutput implements app.Output for testing (captures events).
type MockOutput struct {
	Commands   []string
	Progresses []string
	Successes  []string
	Warnings   []string
	Infos      []string
	Errors     []error
}

func (m *MockOutput) Command(command, action, name string, extra ...any) {
	m.Commands = append(m.Commands, command+"/"+action+"/"+name)
}
func (m *MockOutput) Progress(msg string) { m.Progresses = append(m.Progresses, msg) }
func (m *MockOutput) Success(msg string)  { m.Successes = append(m.Successes, msg) }
func (m *MockOutput) Warning(msg string)  { m.Warnings = append(m.Warnings, msg) }
func (m *MockOutput) Info(msg string)     { m.Infos = append(m.Infos, msg) }
func (m *MockOutput) Error(err error)     { m.Errors = append(m.Errors, err) }
func (m *MockOutput) Writer() io.Writer   { return io.Discard }
