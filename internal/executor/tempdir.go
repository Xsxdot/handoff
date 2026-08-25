// tempdir.go —— handoff executor task-private temporary directory contract.
//
// Boundary: this file only computes a path. Adapter startup and cold-resume
// paths own directory creation; policy code consumes the same query through
// its orchestration caller and does not duplicate the layout rule.
package executor

import "path/filepath"

// TaskTmpDir returns <dataDir>/tmp/<first eight bytes of taskID>.
//
// It performs no I/O and never creates the directory. Short IDs are kept as
// supplied, and an empty ID is left to filepath.Join's existing semantics.
// Adapters create the directory only when starting a new process or cold
// restoring one. The eight-byte shape preserves AF_UNIX path budget: the old
// 61-byte layout plus a 51-byte test suffix exceeded 107 bytes, while the new
// 27-byte directory segment plus that suffix remains 78 bytes.
func TaskTmpDir(dataDir, taskID string) string {
	shortID := taskID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return filepath.Join(dataDir, "tmp", shortID)
}
