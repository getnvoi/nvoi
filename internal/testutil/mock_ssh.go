// Package testutil provides mock implementations of core interfaces for testing.
package testutil

import "github.com/getnvoi/nvoi/internal/testutil/sshmock"

// MockSSH and friends live in internal/testutil/sshmock so pkg/infra's tests
// can use them without dragging in the provider-fake graph (pkg/provider/infra/
// hetzner imports pkg/infra; if testutil pulled in providermocks.go for
// pkg/infra's test compile, the result is an import cycle). The aliases below
// let every other consumer keep its existing `testutil.MockSSH` references.

type (
	MockSSH    = sshmock.MockSSH
	MockResult = sshmock.MockResult
	MockPrefix = sshmock.MockPrefix
	MockUpload = sshmock.MockUpload
)

// NewMockSSH creates a MockSSH with the given exact command mappings.
func NewMockSSH(commands map[string]MockResult) *MockSSH { return sshmock.NewMockSSH(commands) }
