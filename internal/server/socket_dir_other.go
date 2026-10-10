//go:build !unix

package server

import "os"

// fileOwner reports no owner on platforms without unix uids.
func fileOwner(info os.FileInfo) (int, bool) { return 0, false }
