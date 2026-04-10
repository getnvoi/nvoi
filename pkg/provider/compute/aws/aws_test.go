package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── ArchForType ───────────────────────────────────────────────────────────────

func TestArchForType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"t3.small", "amd64"},
		{"t4g.micro", "arm64"},
		{"m6g.large", "arm64"},
		{"c5.xlarge", "amd64"},
		{"a1.medium", "arm64"},
		{"c7g.2xlarge", "arm64"},
		{"r6g.metal", "arm64"},
		{"m5.large", "amd64"},
		{"c6g.nano", "arm64"},
	}
	c := &Client{}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := c.ArchForType(tt.input)
			if got != tt.want {
				t.Errorf("ArchForType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ── instanceFromEC2 ───────────────────────────────────────────────────────────

func TestInstanceFromEC2(t *testing.T) {
	inst := ec2types.Instance{
		InstanceId:       aws.String("i-1234567890abcdef0"),
		PublicIpAddress:  aws.String("54.1.2.3"),
		PrivateIpAddress: aws.String("10.0.1.5"),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateNameRunning,
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("nvoi-test-master")},
			{Key: aws.String("app"), Value: aws.String("test")},
		},
	}

	srv := instanceFromEC2(inst)

	if srv.ID != "i-1234567890abcdef0" {
		t.Errorf("ID = %q, want %q", srv.ID, "i-1234567890abcdef0")
	}
	if srv.Name != "nvoi-test-master" {
		t.Errorf("Name = %q, want %q", srv.Name, "nvoi-test-master")
	}
	if srv.IPv4 != "54.1.2.3" {
		t.Errorf("IPv4 = %q, want %q", srv.IPv4, "54.1.2.3")
	}
	if srv.PrivateIP != "10.0.1.5" {
		t.Errorf("PrivateIP = %q, want %q", srv.PrivateIP, "10.0.1.5")
	}
	if srv.Status != provider.ServerStatus(ec2types.InstanceStateNameRunning) {
		t.Errorf("Status = %q, want %q", srv.Status, ec2types.InstanceStateNameRunning)
	}
}

func TestInstanceFromEC2_NoPublicIP(t *testing.T) {
	inst := ec2types.Instance{
		InstanceId:       aws.String("i-abcdef1234567890"),
		PrivateIpAddress: aws.String("10.0.1.99"),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateNameRunning,
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("nvoi-test-worker")},
		},
	}

	srv := instanceFromEC2(inst)

	if srv.IPv4 != "" {
		t.Errorf("IPv4 = %q, want empty (no public IP)", srv.IPv4)
	}
	if srv.PrivateIP != "10.0.1.99" {
		t.Errorf("PrivateIP = %q, want %q", srv.PrivateIP, "10.0.1.99")
	}
	if srv.Name != "nvoi-test-worker" {
		t.Errorf("Name = %q, want %q", srv.Name, "nvoi-test-worker")
	}
}

// ── volumeFromEC2 ─────────────────────────────────────────────────────────────

func TestVolumeFromEC2(t *testing.T) {
	vol := ec2types.Volume{
		VolumeId:         aws.String("vol-0123456789abcdef0"),
		Size:             aws.Int32(50),
		AvailabilityZone: aws.String("us-east-1a"),
		Tags: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("nvoi-test-data")},
		},
		Attachments: []ec2types.VolumeAttachment{
			{
				InstanceId: aws.String("i-1234567890abcdef0"),
				Device:     aws.String("/dev/xvdf"),
			},
		},
	}

	v := volumeFromEC2(vol)

	if v.ID != "vol-0123456789abcdef0" {
		t.Errorf("ID = %q, want %q", v.ID, "vol-0123456789abcdef0")
	}
	if v.Name != "nvoi-test-data" {
		t.Errorf("Name = %q, want %q", v.Name, "nvoi-test-data")
	}
	if v.Size != 50 {
		t.Errorf("Size = %d, want %d", v.Size, 50)
	}
	if v.Location != "us-east-1a" {
		t.Errorf("Location = %q, want %q", v.Location, "us-east-1a")
	}
	if v.ServerID != "i-1234567890abcdef0" {
		t.Errorf("ServerID = %q, want %q", v.ServerID, "i-1234567890abcdef0")
	}
	if v.DevicePath != "/dev/xvdf" {
		t.Errorf("DevicePath = %q, want %q", v.DevicePath, "/dev/xvdf")
	}
}

func TestVolumeFromEC2_Unattached(t *testing.T) {
	vol := ec2types.Volume{
		VolumeId:         aws.String("vol-unattached"),
		Size:             aws.Int32(20),
		AvailabilityZone: aws.String("eu-west-1b"),
		Tags: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("nvoi-test-detached")},
		},
	}

	v := volumeFromEC2(vol)

	if v.ServerID != "" {
		t.Errorf("ServerID = %q, want empty (unattached)", v.ServerID)
	}
	if v.DevicePath != "" {
		t.Errorf("DevicePath = %q, want empty (unattached)", v.DevicePath)
	}
	if v.Size != 20 {
		t.Errorf("Size = %d, want %d", v.Size, 20)
	}
}

// ── defaultIngressRules + reconciliation ─────────────────────────────────────

func TestBaseIngressRules_RuleCount(t *testing.T) {
	// Base rules: SSH + 4 internal (6443, 10250, 8472, 5000). No HTTP ports.
	rules := baseIngressRules()
	if len(rules) != 5 {
		t.Fatalf("expected 5 base ingress rules, got %d — was a rule added or removed?", len(rules))
	}
}

func TestBaseIngressRules_SSHOpen(t *testing.T) {
	rules := baseIngressRules()
	found := false
	for _, r := range rules {
		if deref32(r.FromPort) == 22 {
			for _, ipRange := range r.IpRanges {
				if deref(ipRange.CidrIp) == "0.0.0.0/0" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("SSH (22) should be open to 0.0.0.0/0 in base rules")
	}
}

func TestBaseIngressRules_NoHTTPPorts(t *testing.T) {
	rules := baseIngressRules()
	for _, r := range rules {
		port := deref32(r.FromPort)
		if port == 80 || port == 443 {
			t.Errorf("port %d should NOT be in base rules — managed by firewall set", port)
		}
	}
}

func TestBaseIngressRules_PrivatePorts(t *testing.T) {
	rules := baseIngressRules()
	privatePorts := map[int32]bool{6443: false, 10250: false, 5000: false}

	for _, r := range rules {
		port := deref32(r.FromPort)
		if _, ok := privatePorts[port]; ok {
			for _, ipRange := range r.IpRanges {
				if deref(ipRange.CidrIp) == utils.PrivateNetworkCIDR {
					privatePorts[port] = true
				}
			}
		}
	}

	for port, found := range privatePorts {
		if !found {
			t.Errorf("port %d should be restricted to 10.0.0.0/16", port)
		}
	}
}

func TestBaseIngressRules(t *testing.T) {
	rules := baseIngressRules()

	if len(rules) != 5 {
		t.Fatalf("expected 5 base rules, got %d", len(rules))
	}

	// Build a lookup: port → (protocol, cidr)
	type ruleKey struct {
		port     int32
		protocol string
	}
	seen := map[ruleKey]string{}
	for _, r := range rules {
		port := deref32(r.FromPort)
		proto := deref(r.IpProtocol)
		cidr := ""
		if len(r.IpRanges) > 0 {
			cidr = deref(r.IpRanges[0].CidrIp)
		}
		seen[ruleKey{port, proto}] = cidr
	}

	// SSH — open by default
	if cidr, ok := seen[ruleKey{22, "tcp"}]; !ok || cidr != "0.0.0.0/0" {
		t.Errorf("SSH (22/tcp) rule missing or wrong CIDR: %q", cidr)
	}

	// HTTP/HTTPS should NOT be present in base rules
	if _, ok := seen[ruleKey{80, "tcp"}]; ok {
		t.Error("HTTP (80) should NOT be in base rules")
	}
	if _, ok := seen[ruleKey{443, "tcp"}]; ok {
		t.Error("HTTPS (443) should NOT be in base rules")
	}

	// Private rules
	if cidr, ok := seen[ruleKey{6443, "tcp"}]; !ok || cidr != utils.PrivateNetworkCIDR {
		t.Errorf("k8s API (6443/tcp) rule missing or wrong CIDR: %q", cidr)
	}
	if cidr, ok := seen[ruleKey{10250, "tcp"}]; !ok || cidr != utils.PrivateNetworkCIDR {
		t.Errorf("kubelet (10250/tcp) rule missing or wrong CIDR: %q", cidr)
	}
	if cidr, ok := seen[ruleKey{8472, "udp"}]; !ok || cidr != utils.PrivateNetworkCIDR {
		t.Errorf("VXLAN (8472/udp) rule missing or wrong CIDR: %q", cidr)
	}
	if cidr, ok := seen[ruleKey{5000, "tcp"}]; !ok || cidr != utils.PrivateNetworkCIDR {
		t.Errorf("registry (5000/tcp) rule missing or wrong CIDR: %q", cidr)
	}
}

// ── nvoiTags ──────────────────────────────────────────────────────────────────

func TestNvoiTags(t *testing.T) {
	labels := map[string]string{"app": "myapp", "env": "prod"}
	tags := nvoiTags("nvoi-myapp-prod-master", labels)

	// Should have Name + 2 labels = 3 tags
	if len(tags) < 3 {
		t.Fatalf("expected at least 3 tags, got %d", len(tags))
	}

	found := map[string]string{}
	for _, tag := range tags {
		found[deref(tag.Key)] = deref(tag.Value)
	}

	if found["Name"] != "nvoi-myapp-prod-master" {
		t.Errorf("Name tag = %q, want %q", found["Name"], "nvoi-myapp-prod-master")
	}
	if found["app"] != "myapp" {
		t.Errorf("app tag = %q, want %q", found["app"], "myapp")
	}
	if found["env"] != "prod" {
		t.Errorf("env tag = %q, want %q", found["env"], "prod")
	}
}

func TestNvoiTags_NoLabels(t *testing.T) {
	tags := nvoiTags("nvoi-test-master", nil)

	if len(tags) != 1 {
		t.Fatalf("expected 1 tag (Name only), got %d", len(tags))
	}
	if deref(tags[0].Key) != "Name" {
		t.Errorf("tag key = %q, want %q", deref(tags[0].Key), "Name")
	}
	if deref(tags[0].Value) != "nvoi-test-master" {
		t.Errorf("tag value = %q, want %q", deref(tags[0].Value), "nvoi-test-master")
	}
}

// ── deref / deref32 ──────────────────────────────────────────────────────────

func TestDeref(t *testing.T) {
	if got := deref(nil); got != "" {
		t.Errorf("deref(nil) = %q, want %q", got, "")
	}
	s := "hello"
	if got := deref(&s); got != "hello" {
		t.Errorf("deref(&%q) = %q, want %q", s, got, "hello")
	}
}

func TestDeref32(t *testing.T) {
	if got := deref32(nil); got != 0 {
		t.Errorf("deref32(nil) = %d, want %d", got, 0)
	}
	var n int32 = 42
	if got := deref32(&n); got != 42 {
		t.Errorf("deref32(&42) = %d, want %d", got, 42)
	}
}

// ── ResolveCompute ────────────────────────────────────────────────────────────

func TestResolveCompute(t *testing.T) {
	// init() in register.go registers "aws" — verify it resolves with valid creds.
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
	}
	p, err := provider.ResolveCompute("aws", creds)
	if err != nil {
		t.Fatalf("ResolveCompute with valid creds: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveCompute returned nil provider")
	}
}

func TestResolveCompute_MissingCredentials(t *testing.T) {
	tests := []struct {
		name  string
		creds map[string]string
		want  string // substring expected in error
	}{
		{
			name:  "missing access_key_id",
			creds: map[string]string{"secret_access_key": "secret", "region": "us-east-1"},
			want:  "access_key_id",
		},
		{
			name:  "missing secret_access_key",
			creds: map[string]string{"access_key_id": "AKIA...", "region": "us-east-1"},
			want:  "secret_access_key",
		},
		{
			name:  "missing region",
			creds: map[string]string{"access_key_id": "AKIA...", "secret_access_key": "secret"},
			want:  "region",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := provider.ResolveCompute("aws", tt.creds)
			if err == nil {
				t.Fatal("expected error for missing credentials")
			}
			if !contains(err.Error(), tt.want) {
				t.Errorf("error %q should mention %q", err.Error(), tt.want)
			}
		})
	}
}

// ── Volume shrink rejection ──────────────────────────────────────────────────

func TestVolumeFromEC2_SizeConversion(t *testing.T) {
	// Verify that deref32 correctly extracts size for resize comparison
	var size int32 = 30
	vol := ec2types.Volume{
		VolumeId: aws.String("vol-test"),
		Size:     &size,
	}
	if got := int(deref32(vol.Size)); got != 30 {
		t.Errorf("deref32(Size) = %d, want 30", got)
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
