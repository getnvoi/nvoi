package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// AMI aliases map generic image names to Ubuntu AMI name patterns.
var amiAliases = map[string]string{
	"ubuntu-24.04": "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-%s-server-*",
	"ubuntu-22.04": "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-%s-server-*",
}

func instanceFromEC2(inst ec2types.Instance) *provider.Server {
	srv := &provider.Server{
		ID:     deref(inst.InstanceId),
		Status: provider.ServerStatus(inst.State.Name),
	}
	for _, tag := range inst.Tags {
		if deref(tag.Key) == "Name" {
			srv.Name = deref(tag.Value)
		}
	}
	if inst.PublicIpAddress != nil {
		srv.IPv4 = *inst.PublicIpAddress
	}
	if inst.PrivateIpAddress != nil {
		srv.PrivateIP = *inst.PrivateIpAddress
	}
	return srv
}

func (c *Client) EnsureServer(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error) {
	existing, err := c.findInstanceByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return instanceFromEC2(*existing), nil
	}

	// Resolve AMI
	amiID, err := c.resolveAMI(ctx, req.Image, req.ServerType)
	if err != nil {
		return nil, fmt.Errorf("resolve ami: %w", err)
	}

	// Ensure VPC + networking
	vpcID, subnetID, err := c.ensureVPC(ctx, req.NetworkName, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}

	// Ensure security group
	sgID, err := c.ensureSecurityGroup(ctx, req.FirewallName, vpcID, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("firewall: %w", err)
	}

	// Launch instance
	input := &ec2.RunInstancesInput{
		ImageId:           aws.String(amiID),
		InstanceType:      ec2types.InstanceType(req.ServerType),
		MinCount:          aws.Int32(1),
		MaxCount:          aws.Int32(1),
		SubnetId:          aws.String(subnetID),
		SecurityGroupIds:  []string{sgID},
		TagSpecifications: tagSpec(ec2types.ResourceTypeInstance, req.Name, req.Labels),
	}
	if req.UserData != "" {
		input.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(req.UserData)))
	}

	result, err := c.ec2.RunInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}
	if len(result.Instances) == 0 {
		return nil, fmt.Errorf("no instance created")
	}

	instanceID := deref(result.Instances[0].InstanceId)

	// Wait for running
	if err := utils.Poll(ctx, 5*time.Second, 5*time.Minute, func() (bool, error) {
		resp, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			return false, nil
		}
		if len(resp.Reservations) > 0 && len(resp.Reservations[0].Instances) > 0 {
			return resp.Reservations[0].Instances[0].State.Name == ec2types.InstanceStateNameRunning, nil
		}
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("instance %s did not start: %w", instanceID, err)
	}

	// Re-fetch for IPs
	resp, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return nil, fmt.Errorf("refetch instance: %w", err)
	}
	if len(resp.Reservations) == 0 || len(resp.Reservations[0].Instances) == 0 {
		return nil, fmt.Errorf("instance %s not found after create", instanceID)
	}

	return instanceFromEC2(resp.Reservations[0].Instances[0]), nil
}

func (c *Client) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	inst, err := c.findInstanceByName(ctx, req.Name)
	if err != nil {
		return err
	}
	if inst == nil {
		return nil // idempotent — already gone
	}

	instanceID := deref(inst.InstanceId)

	// Detach volumes before termination — AWS volumes survive instance termination
	for _, bdm := range inst.BlockDeviceMappings {
		if bdm.Ebs == nil || bdm.Ebs.VolumeId == nil {
			continue
		}
		volID := deref(bdm.Ebs.VolumeId)
		c.ec2.DetachVolume(ctx, &ec2.DetachVolumeInput{VolumeId: aws.String(volID)})
		_ = c.waitForVolumeAvailable(ctx, volID)
	}

	// Terminate instance
	_, err = c.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("terminate instance: %w", err)
	}

	// Wait for terminated
	if err := utils.Poll(ctx, 5*time.Second, 2*time.Minute, func() (bool, error) {
		resp, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			return true, nil // likely already gone
		}
		if len(resp.Reservations) > 0 && len(resp.Reservations[0].Instances) > 0 {
			return resp.Reservations[0].Instances[0].State.Name == ec2types.InstanceStateNameTerminated, nil
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("instance %s did not terminate: %w", instanceID, err)
	}

	return nil
}

func (c *Client) ListServers(ctx context.Context, labels map[string]string) ([]*provider.Server, error) {
	filters := []ec2types.Filter{
		{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
	}
	for k, v := range labels {
		filters = append(filters, ec2types.Filter{
			Name: aws.String("tag:" + k), Values: []string{v},
		})
	}

	resp, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	var servers []*provider.Server
	for _, res := range resp.Reservations {
		for _, inst := range res.Instances {
			servers = append(servers, instanceFromEC2(inst))
		}
	}
	return servers, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func (c *Client) findInstanceByName(ctx context.Context, name string) (*ec2types.Instance, error) {
	resp, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:Name"), Values: []string{name}},
			{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
		},
	})
	if err != nil {
		return nil, err
	}
	for _, res := range resp.Reservations {
		if len(res.Instances) > 0 {
			return &res.Instances[0], nil
		}
	}
	return nil, nil
}

func (c *Client) resolveAMI(ctx context.Context, image, instanceType string) (string, error) {
	if strings.HasPrefix(image, "ami-") {
		return image, nil
	}

	arch := "amd64"
	if c.ArchForType(instanceType) == "arm64" {
		arch = "arm64"
	}

	pattern, ok := amiAliases[image]
	if !ok {
		return "", fmt.Errorf("unknown image %q — use ami-xxx or one of: ubuntu-24.04, ubuntu-22.04", image)
	}
	namePattern := fmt.Sprintf(pattern, arch)

	resp, err := c.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"099720109477"}, // Canonical
		Filters: []ec2types.Filter{
			{Name: aws.String("name"), Values: []string{namePattern}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("lookup ami %q: %w", image, err)
	}
	if len(resp.Images) == 0 {
		return "", fmt.Errorf("no AMI found for %q (arch %s) in %s", image, arch, c.region)
	}

	// Sort by creation date, pick latest
	sort.Slice(resp.Images, func(i, j int) bool {
		return deref(resp.Images[i].CreationDate) > deref(resp.Images[j].CreationDate)
	})

	return deref(resp.Images[0].ImageId), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
