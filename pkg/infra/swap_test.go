package infra

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestEnsureSwap_AlreadyActive(t *testing.T) {
	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		"swapon --show --noheadings": {Output: []byte("/swapfile file 1024M 0B -2\n")},
	})

	err := EnsureSwap(context.Background(), mock)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// Should not run any setup commands
	if len(mock.Calls) != 1 {
		t.Errorf("expected 1 call (swapon check), got %d: %v", len(mock.Calls), mock.Calls)
	}
}

func TestEnsureSwap_CreatesSwap(t *testing.T) {
	mock := &testutil.MockSSH{
		Commands: map[string]testutil.MockResult{
			"swapon --show --noheadings":   {Output: []byte("")},             // no swap
			"df --output=size / | tail -1": {Output: []byte("  41943040\n")}, // ~40GB in KB
		},
		Prefixes: []testutil.MockPrefix{
			{Prefix: "sudo fallocate", Result: testutil.MockResult{}},
			{Prefix: "sudo chmod", Result: testutil.MockResult{}},
			{Prefix: "sudo mkswap", Result: testutil.MockResult{}},
			{Prefix: "sudo swapon", Result: testutil.MockResult{}},
			{Prefix: "sudo grep", Result: testutil.MockResult{}},
		},
	}

	err := EnsureSwap(context.Background(), mock)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify fallocate was called with correct size
	// 40GB → SwapSize(40) = 2048MB
	for _, call := range mock.Calls {
		if call == "sudo fallocate -l 2048M /swapfile" {
			return // correct
		}
	}
	t.Errorf("expected 'sudo fallocate -l 2048M /swapfile', calls: %v", mock.Calls)
}

func TestEnsureSwap_SmallDisk(t *testing.T) {
	mock := &testutil.MockSSH{
		Commands: map[string]testutil.MockResult{
			"swapon --show --noheadings":   {Output: []byte("")},
			"df --output=size / | tail -1": {Output: []byte("  5242880\n")}, // ~5GB in KB
		},
		Prefixes: []testutil.MockPrefix{
			{Prefix: "sudo fallocate", Result: testutil.MockResult{}},
			{Prefix: "sudo chmod", Result: testutil.MockResult{}},
			{Prefix: "sudo mkswap", Result: testutil.MockResult{}},
			{Prefix: "sudo swapon", Result: testutil.MockResult{}},
			{Prefix: "sudo grep", Result: testutil.MockResult{}},
		},
	}

	err := EnsureSwap(context.Background(), mock)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// 5GB → SwapSize(5) = 512MB (floor)
	for _, call := range mock.Calls {
		if call == "sudo fallocate -l 512M /swapfile" {
			return // correct
		}
	}
	t.Errorf("expected fallocate -l 512M, calls: %v", mock.Calls)
}

func TestEnsureSwap_DfFails_NoError(t *testing.T) {
	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		"swapon --show --noheadings":   {Output: []byte("")},
		"df --output=size / | tail -1": {Err: fmt.Errorf("df failed")},
	})

	err := EnsureSwap(context.Background(), mock)
	if err != nil {
		t.Fatalf("df failure should not be fatal, got: %v", err)
	}
}

func TestSwapSize_Table(t *testing.T) {
	tests := []struct {
		disk int
		want int
	}{
		{0, 1024},   // default 20GB → 1024MB
		{5, 512},    // floor
		{10, 512},   // 512MB
		{20, 1024},  // 1GB
		{40, 2048},  // 2GB
		{100, 2048}, // cap
		{-1, 1024},  // negative → default
	}
	for _, tt := range tests {
		got := SwapSize(tt.disk)
		if got != tt.want {
			t.Errorf("SwapSize(%d) = %d, want %d", tt.disk, got, tt.want)
		}
	}
}
