package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateV0toV1_FullMigration(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `# My AutoPR config
log_level = "info"

[daemon]
webhook_port = 9847
max_workers = 3
sync_interval = "5m"

[llm]
provider = "codex"
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	got := string(data)

	// config_version = 1 should be present.
	if !strings.Contains(got, "config_version = 1") {
		t.Fatalf("expected config_version = 1 in output:\n%s", got)
	}
	// CI fields should be present.
	if !strings.Contains(got, `ci_check_interval = "30s"`) {
		t.Fatalf("expected ci_check_interval in output:\n%s", got)
	}
	if !strings.Contains(got, `ci_check_timeout = "30m"`) {
		t.Fatalf("expected ci_check_timeout in output:\n%s", got)
	}
	// Original content preserved.
	if !strings.Contains(got, "# My AutoPR config") {
		t.Fatalf("expected original comment preserved:\n%s", got)
	}
	if !strings.Contains(got, `log_level = "info"`) {
		t.Fatalf("expected log_level preserved:\n%s", got)
	}
}

func TestMigratePreservesExistingValues(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `log_level = "info"

[daemon]
webhook_port = 9847
ci_check_interval = "1m"
ci_check_timeout = "45m"
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	// Existing values must be preserved, not overwritten.
	if !strings.Contains(got, `ci_check_interval = "1m"`) {
		t.Fatalf("expected existing ci_check_interval preserved:\n%s", got)
	}
	if !strings.Contains(got, `ci_check_timeout = "45m"`) {
		t.Fatalf("expected existing ci_check_timeout preserved:\n%s", got)
	}
	// Should NOT contain the default values.
	if strings.Contains(got, `ci_check_interval = "30s"`) {
		t.Fatalf("should not have overwritten ci_check_interval:\n%s", got)
	}
}

func TestMigrateNoDaemonSection(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `log_level = "info"

[llm]
provider = "codex"
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	// config_version should still be stamped.
	if !strings.Contains(got, "config_version = 1") {
		t.Fatalf("expected config_version = 1:\n%s", got)
	}
	// CI fields should NOT be inserted since there's no [daemon] section.
	if strings.Contains(got, "ci_check_interval") {
		t.Fatalf("should not insert ci_check_interval without [daemon] section:\n%s", got)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `# comment
log_level = "info"

[daemon]
webhook_port = 9847
sync_interval = "5m"
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// First migration.
	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	firstData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read after first: %v", err)
	}

	// Count backup files before second migration.
	entries1, _ := os.ReadDir(tmp)
	bakCount1 := countBackups(entries1)

	// Second migration — should be a no-op.
	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	secondData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read after second: %v", err)
	}

	if string(firstData) != string(secondData) {
		t.Fatalf("second migration changed file:\n--- first ---\n%s\n--- second ---\n%s", firstData, secondData)
	}

	// No new backup should be created.
	entries2, _ := os.ReadDir(tmp)
	bakCount2 := countBackups(entries2)
	if bakCount2 != bakCount1 {
		t.Fatalf("expected no new backup, got %d → %d", bakCount1, bakCount2)
	}
}

func TestMigrateCreatesBackup(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `log_level = "info"

[daemon]
webhook_port = 9847
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	bakCount := countBackups(entries)
	if bakCount != 1 {
		t.Fatalf("expected 1 backup file, got %d", bakCount)
	}

	// Verify backup contains original content.
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.toml.bak.") {
			backupData, err := os.ReadFile(filepath.Join(tmp, e.Name()))
			if err != nil {
				t.Fatalf("read backup: %v", err)
			}
			if string(backupData) != input {
				t.Fatalf("backup content mismatch:\nexpected:\n%s\ngot:\n%s", input, backupData)
			}
		}
	}
}

func TestMigratePreservesComments(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `# Top-level comment about the file
# Second line of comment

log_level = "info"  # inline comment

[daemon]
webhook_port = 9847  # port comment
# A comment about workers
max_workers = 3

[llm]
provider = "codex"
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	// All original comments should survive.
	for _, want := range []string{
		"# Top-level comment about the file",
		"# Second line of comment",
		"# inline comment",
		"# port comment",
		"# A comment about workers",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected comment %q preserved in:\n%s", want, got)
		}
	}
}

func TestMigrateAlreadyCurrent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `config_version = 1
log_level = "info"

[daemon]
webhook_port = 9847
ci_check_interval = "30s"
ci_check_timeout = "30m"
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// No backup should be created.
	entries, _ := os.ReadDir(tmp)
	if bakCount := countBackups(entries); bakCount != 0 {
		t.Fatalf("expected no backup for current config, got %d", bakCount)
	}

	// File should be unchanged.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != input {
		t.Fatalf("file was modified when it shouldn't have been:\n%s", data)
	}
}

func TestMigrateNonExistentFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "does-not-exist.toml")

	// Should return nil, not error.
	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("expected nil for non-existent file, got: %v", err)
	}
}

func TestMigratePreservesFilePermissions(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `log_level = "info"

[daemon]
webhook_port = 9847
`
	if err := os.WriteFile(cfgPath, []byte(input), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Migrated file should preserve permissions.
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected config permissions 0600, got %04o", perm)
	}

	// Backup file should also preserve source permissions.
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			bakInfo, err := os.Stat(filepath.Join(tmp, e.Name()))
			if err != nil {
				t.Fatalf("stat backup: %v", err)
			}
			if perm := bakInfo.Mode().Perm(); perm != 0o600 {
				t.Fatalf("expected backup permissions 0600, got %04o", perm)
			}
		}
	}
}

func TestMigrateBackupStripsGroupOtherBits(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	input := `log_level = "info"

[daemon]
webhook_port = 9847
`
	// Source file is world-readable (0644).
	if err := os.WriteFile(cfgPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := MigrateConfigFile(cfgPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Migrated config keeps original permissions.
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("expected config permissions 0644, got %04o", perm)
	}

	// Backup should have group/other bits stripped (0644 & 0700 = 0600).
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			bakInfo, err := os.Stat(filepath.Join(tmp, e.Name()))
			if err != nil {
				t.Fatalf("stat backup: %v", err)
			}
			if perm := bakInfo.Mode().Perm(); perm != 0o600 {
				t.Fatalf("expected backup permissions 0600, got %04o", perm)
			}
		}
	}
}

func countBackups(entries []os.DirEntry) int {
	count := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			count++
		}
	}
	return count
}
