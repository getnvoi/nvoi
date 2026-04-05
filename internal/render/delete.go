package render

import (
	"errors"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// HandleDeleteResult renders the appropriate success message for a delete operation
// and returns nil (success) or a real error. The caller should return this directly.
//
//   - nil → "✓ deleted"
//   - ErrNotFound → "✓ already gone"
//   - ErrNoMaster → "✓ cluster gone"
//   - other → returned as-is (error)
func HandleDeleteResult(err error, out pkgcore.Output) error {
	switch {
	case err == nil:
		out.Success("deleted")
		return nil
	case errors.Is(err, utils.ErrNotFound):
		out.Success("already gone")
		return nil
	case errors.Is(err, pkgcore.ErrNoMaster):
		out.Success("cluster gone")
		return nil
	default:
		return err
	}
}
