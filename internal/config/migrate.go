package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// CurrentConfigVersion is the latest config schema version. Bump this only
// when adding new fields or changing the config structure.
const CurrentConfigVersion = 1

// MigrationFunc transforms raw TOML bytes from one version to the next.
type MigrationFunc func([]byte) ([]byte, error)

// migrations maps from-version → transform function.
// Each func upgrades from version N to N+1.
var migrations = map[int]MigrationFunc{
	0: migrateV0toV1,
}

// MigrateConfigFile reads the config at path, detects its schema version,
// and applies any pending migrations. A timestamped backup is created before
// any changes are written. Returns nil if the config is already current or
// if the file does not exist.
func MigrateConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config for migration: %w", err)
	}

	ver := detectConfigVersion(data)
	if ver >= CurrentConfigVersion {
		return nil
	}

	// Capture original file permissions.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	mode := info.Mode().Perm()

	// Create backup before modifying (preserving source permissions).
	if err := backupConfigFile(path, data, mode); err != nil {
		return fmt.Errorf("backup config: %w", err)
	}

	// Apply migrations sequentially.
	current := data
	for v := ver; v < CurrentConfigVersion; v++ {
		fn, ok := migrations[v]
		if !ok {
			return fmt.Errorf("no migration registered for version %d → %d", v, v+1)
		}
		current, err = fn(current)
		if err != nil {
			return fmt.Errorf("migrate v%d→v%d: %w", v, v+1, err)
		}
	}

	if err := os.WriteFile(path, current, mode); err != nil {
		return fmt.Errorf("write migrated config: %w", err)
	}

	slog.Info("config migrated", "from_version", ver, "to_version", CurrentConfigVersion, "path", path)
	return nil
}

// detectConfigVersion decodes only the config_version field from raw TOML.
// Returns 0 if the field is absent.
func detectConfigVersion(data []byte) int {
	var v struct {
		ConfigVersion int `toml:"config_version"`
	}
	if _, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return 0
	}
	return v.ConfigVersion
}

// backupConfigFile writes data to <path>.bak.<YYYYMMdd-HHmmss>,
// preserving the source file's permissions.
func backupConfigFile(path string, data []byte, mode os.FileMode) error {
	// Keep backups at most as permissive as the source config, never exposing
	// credentials to group/other readers.
	backupMode := mode & 0o700
	if backupMode == 0 {
		backupMode = 0o600
	}
	ts := time.Now().Format("20060102-150405")
	backupPath := fmt.Sprintf("%s.bak.%s", path, ts)
	return os.WriteFile(backupPath, data, backupMode)
}

// migrateV0toV1 inserts ci_check_interval and ci_check_timeout into the
// [daemon] section (if present) and stamps config_version = 1.
func migrateV0toV1(data []byte) ([]byte, error) {
	result := data

	// Insert CI fields into [daemon] section if it exists.
	result = tomlInsertInSection(result, "daemon", []keyValue{
		{Key: "ci_check_interval", Value: `"30s"`, Comment: "# How often to poll CI check-runs"},
		{Key: "ci_check_timeout", Value: `"30m"`, Comment: "# Max wait for CI checks before rejecting"},
	})

	// Stamp config_version = 1.
	result = tomlSetConfigVersion(result, 1)

	return result, nil
}

// keyValue represents a TOML key = value pair to insert.
type keyValue struct {
	Key     string
	Value   string
	Comment string // optional inline comment
}

// tomlSetConfigVersion sets or inserts config_version at the top level.
func tomlSetConfigVersion(data []byte, version int) []byte {
	versionLine := fmt.Sprintf("config_version = %d", version)
	re := regexp.MustCompile(`(?m)^config_version\s*=\s*\d+`)
	if re.Match(data) {
		return re.ReplaceAll(data, []byte(versionLine))
	}
	return tomlInsertTopLevel(data, versionLine)
}

// tomlInsertTopLevel inserts a line after leading comments/blank lines,
// before the first key or section header.
func tomlInsertTopLevel(data []byte, line string) []byte {
	lines := strings.Split(string(data), "\n")
	insertIdx := 0

	// Skip leading comments and blank lines.
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			insertIdx = i + 1
			continue
		}
		break
	}

	// Insert line with a trailing blank line for readability.
	result := make([]string, 0, len(lines)+2)
	result = append(result, lines[:insertIdx]...)
	result = append(result, line)
	// Add blank line separator if the next line is not blank.
	if insertIdx < len(lines) && strings.TrimSpace(lines[insertIdx]) != "" {
		result = append(result, "")
	}
	result = append(result, lines[insertIdx:]...)

	return []byte(strings.Join(result, "\n"))
}

// tomlInsertInSection finds a [section] header and appends key-value pairs
// after the last key in that section. Skips keys that are already present.
func tomlInsertInSection(data []byte, section string, kvs []keyValue) []byte {
	lines := strings.Split(string(data), "\n")
	sectionHeader := fmt.Sprintf("[%s]", section)

	inSection := false
	insertIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == sectionHeader {
			inSection = true
			insertIdx = i + 1
			continue
		}

		if inSection {
			// Another section or array-of-tables header ends the current section.
			if strings.HasPrefix(trimmed, "[") {
				break
			}
			// Track last non-empty, non-comment line as insert point.
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				insertIdx = i + 1
			}
			// Also advance past comments and blank lines within the section.
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				if insertIdx <= i {
					insertIdx = i + 1
				}
			}
		}
	}

	if insertIdx < 0 {
		// Section not found — nothing to insert.
		return data
	}

	// Build lines to insert, skipping keys already present.
	var toInsert []string
	for _, kv := range kvs {
		if sectionContainsKey(lines, section, kv.Key) {
			continue
		}
		entry := fmt.Sprintf("%s = %s", kv.Key, kv.Value)
		if kv.Comment != "" {
			entry += "   " + kv.Comment
		}
		toInsert = append(toInsert, entry)
	}

	if len(toInsert) == 0 {
		return data
	}

	result := make([]string, 0, len(lines)+len(toInsert))
	result = append(result, lines[:insertIdx]...)
	result = append(result, toInsert...)
	result = append(result, lines[insertIdx:]...)

	return []byte(strings.Join(result, "\n"))
}

// sectionContainsKey checks if a key exists within a TOML section.
func sectionContainsKey(lines []string, section, key string) bool {
	sectionHeader := fmt.Sprintf("[%s]", section)
	inSection := false
	keyPattern := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == sectionHeader {
			inSection = true
			continue
		}

		if inSection {
			if strings.HasPrefix(trimmed, "[") {
				return false
			}
			if keyPattern.MatchString(line) {
				return true
			}
		}
	}
	return false
}
