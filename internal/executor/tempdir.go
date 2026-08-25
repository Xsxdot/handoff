package executor

import "path/filepath"

// TaskTmpDir returns the task-private temporary directory assigned by handoff.
//
// The returned path is deliberately outside the task worktree and task
// directory: it is <dataDir>/tmp/<first eight bytes of taskID>.  The short
// shape is part of the executor contract because claudecode permission tests
// create a Unix socket below the process temporary directory.
func TaskTmpDir(dataDir, taskID string) string {
	shortID := taskID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return filepath.Join(dataDir, "tmp", shortID)
}
