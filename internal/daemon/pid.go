package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// WritePID creates the PID file atomically with O_EXCL.
func WritePID(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// Check if the existing PID is stale.
			if cleanStalePID(path) {
				// Retry after cleaning stale PID.
				f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
				if err != nil {
					return fmt.Errorf("create pid file after stale cleanup: %w", err)
				}
			} else {
				return fmt.Errorf("daemon already running (pid file %s exists)", path)
			}
		} else {
			return fmt.Errorf("create pid file: %w", err)
		}
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	return nil
}

// ReadPID reads the PID from the file.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}
	return pid, nil
}

// IsRunning checks if the daemon process is alive.
func IsRunning(path string) bool {
	pid, err := ReadPID(path)
	if err != nil {
		return false
	}
	return processAlive(pid)
}

// RemovePID removes the PID file.
func RemovePID(path string) {
	_ = os.Remove(path)
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func cleanStalePID(path string) bool {
	pid, err := ReadPID(path)
	if err != nil {
		_ = os.Remove(path)
		return true
	}
	if !processAlive(pid) {
		_ = os.Remove(path)
		return true
	}
	return false
}
