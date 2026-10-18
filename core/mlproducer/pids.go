package mlproducer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// AgentPIDsPath is <dataDir>/fleet/agent_pids: the harness process's pid
// for the lifetime of the app (spec §1 rule 3). Files the agent writes are
// written by this process, so the daemon (sigil, spec §9) can skip writes
// by a listed pid. One pid per line; this build writes exactly one.
func AgentPIDsPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "agent_pids")
}

// WriteAgentPIDs records pid atomically (tmp + rename, 0600; the fleet
// directory is created 0700 when missing).
func WriteAgentPIDs(dataDir string, pid int) error {
	if dataDir == "" {
		return errors.New("mlproducer: agent_pids: empty data dir")
	}
	path := AgentPIDsPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mlproducer: agent_pids mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("mlproducer: agent_pids write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mlproducer: agent_pids rename: %w", err)
	}
	return nil
}

// RemoveAgentPIDs deletes the file (clean shutdown). Missing is not an
// error.
func RemoveAgentPIDs(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	if err := os.Remove(AgentPIDsPath(dataDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mlproducer: agent_pids remove: %w", err)
	}
	return nil
}
