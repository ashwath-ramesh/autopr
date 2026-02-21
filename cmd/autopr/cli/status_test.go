package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"autopr/internal/db"

	"github.com/spf13/cobra"
)

type statusSeed struct {
	state  string
	count  int
	merged int
}

type statusJSONOutput struct {
	Running   bool           `json:"running"`
	PID       string         `json:"pid"`
	JobCounts map[string]int `json:"job_counts"`
}

func TestRunStatusJSONOutputsNormalizedJobCounts(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")

	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 1},
		{state: "planning", count: 2},
		{state: "implementing", count: 1},
		{state: "reviewing", count: 1},
		{state: "testing", count: 1},
		{state: "ready", count: 1},
		{state: "failed", count: 1},
		{state: "cancelled", count: 1},
		{state: "rejected", count: 1},
		{state: "approved", count: 3, merged: 1},
	})

	out := runStatusWithTestConfig(t, cfgPath, true, false)

	var decoded statusJSONOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if decoded.Running {
		t.Fatal("expected running=false")
	}
	if decoded.PID != "" {
		t.Fatalf("expected empty PID, got %q", decoded.PID)
	}
	if _, ok := decoded.JobCounts["queued"]; !ok {
		t.Fatal("missing queued in job_counts")
	}
	expected := map[string]int{
		"queued":       1,
		"planning":     2,
		"implementing": 1,
		"reviewing":    1,
		"testing":      1,
		"needs_pr":     1,
		"failed":       1,
		"cancelled":    1,
		"pr_created":   2,
		"merged":       1,
		"rejected":     1,
	}
	for key, value := range expected {
		got, ok := decoded.JobCounts[key]
		if !ok {
			t.Fatalf("missing %q in job_counts", key)
		}
		if got != value {
			t.Fatalf("job_counts[%q]: expected %d, got %d", key, value, got)
		}
	}
}

func TestRunStatusTableOutputUnchanged(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")

	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 1},
		{state: "planning", count: 1},
		{state: "ready", count: 1},
		{state: "approved", count: 1, merged: 0},
	})

	out := runStatusWithTestConfig(t, cfgPath, false, false)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"Daemon: stopped",
		"",
		"Pipeline:  1 queued · 1 active",
		"Active:    1 planning · 0 implementing · 0 reviewing · 0 testing",
		"Output:    1 needs_pr · 0 merged · 1 pr_created",
	}
	if len(lines) != len(expected) {
		t.Fatalf("unexpected output lines (%d): %q", len(lines), out)
	}
	for i, line := range expected {
		if lines[i] != line {
			t.Fatalf("line %d mismatch: expected %q, got %q", i, line, lines[i])
		}
	}
}

func TestRunStatusShortOutputStopped(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)

	out := runStatusWithTestConfig(t, cfgPath, false, true)
	if got := strings.TrimSpace(out); got != "stopped | 0 queued, 0 active" {
		t.Fatalf("unexpected short output: %q", got)
	}
}

func TestRunStatusShortOutputRunning(t *testing.T) {
	tmp := t.TempDir()
	pidPath := filepath.Join(tmp, "autopr.pid")
	cfgPath := writeStatusConfigWithPID(t, tmp, pidPath)
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := runStatusWithTestConfig(t, cfgPath, false, true)
	if got := strings.TrimSpace(out); got != "running | 0 queued, 0 active" {
		t.Fatalf("unexpected short output: %q", got)
	}
}

func TestRunStatusShortOutputActiveCountIncludesRebasingAndResolvingConflicts(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")

	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 2},
		{state: "planning", count: 1},
		{state: "implementing", count: 2},
		{state: "reviewing", count: 3},
		{state: "testing", count: 4},
		{state: "rebasing", count: 1},
		{state: "resolving_conflicts", count: 5},
	})

	out := runStatusWithTestConfig(t, cfgPath, false, true)
	if got := strings.TrimSpace(out); got != "stopped | 2 queued, 16 active" {
		t.Fatalf("unexpected short output: %q", got)
	}
}

func TestRunStatusJSONHasPriorityOverShort(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)

	out := runStatusWithTestConfig(t, cfgPath, true, true)
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "{") {
		t.Fatalf("expected JSON output, got %q", out)
	}
}

func TestRunStatusTableOutputNoJobSectionsForZeroCounts(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)

	out := runStatusWithTestConfig(t, cfgPath, false, false)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected daemon-only output, got %d lines: %q", len(lines), out)
	}
	if lines[0] != "Daemon: stopped" {
		t.Fatalf("unexpected daemon line: %q", lines[0])
	}
}

func TestRunStatusTableOutputSkipsZeroSections(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")

	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "planning", count: 2},
		{state: "testing", count: 1},
		{state: "failed", count: 3},
	})

	out := runStatusWithTestConfig(t, cfgPath, false, false)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"Daemon: stopped",
		"",
		"Pipeline:  0 queued · 3 active",
		"Active:    2 planning · 0 implementing · 0 reviewing · 1 testing",
		"Problems:  3 failed · 0 rejected · 0 cancelled",
	}
	if len(lines) != len(expected) {
		t.Fatalf("unexpected output lines (%d): %q", len(lines), out)
	}
	for i, line := range expected {
		if lines[i] != line {
			t.Fatalf("line %d mismatch: expected %q, got %q", i, line, lines[i])
		}
	}
}

func TestRunStatusTableOutputIncludesAllSections(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")

	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 2},
		{state: "planning", count: 1},
		{state: "implementing", count: 1},
		{state: "reviewing", count: 2},
		{state: "testing", count: 3},
		{state: "ready", count: 4},
		{state: "failed", count: 1},
		{state: "rejected", count: 2},
		{state: "cancelled", count: 3},
		{state: "approved", count: 5, merged: 2},
	})

	out := runStatusWithTestConfig(t, cfgPath, false, false)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"Daemon: stopped",
		"",
		"Pipeline:  2 queued · 7 active",
		"Active:    1 planning · 1 implementing · 2 reviewing · 3 testing",
		"Output:    4 needs_pr · 2 merged · 3 pr_created",
		"Problems:  1 failed · 2 rejected · 3 cancelled",
	}
	if len(lines) != len(expected) {
		t.Fatalf("unexpected output lines (%d): %q", len(lines), out)
	}
	for i, line := range expected {
		if lines[i] != line {
			t.Fatalf("line %d mismatch: expected %q, got %q", i, line, lines[i])
		}
	}
}

func TestRunStatusJSONNoPidFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)

	out := runStatusWithTestConfig(t, cfgPath, true, false)

	var decoded statusJSONOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if decoded.Running {
		t.Fatalf("expected running=false")
	}
	if decoded.PID != "" {
		t.Fatalf("expected empty PID, got %q", decoded.PID)
	}
}

func TestRunStatusJSONBadPidFile(t *testing.T) {
	tmp := t.TempDir()
	pidPath := filepath.Join(tmp, "autopr.pid")
	cfgPath := writeStatusConfigWithPID(t, tmp, pidPath)
	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write bad pid file: %v", err)
	}

	out := runStatusWithTestConfig(t, cfgPath, true, false)

	var decoded statusJSONOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if decoded.Running {
		t.Fatalf("expected running=false")
	}
	if decoded.PID != "not-a-number" {
		t.Fatalf("expected PID to be preserved as %q, got %q", "not-a-number", decoded.PID)
	}
}

func TestRunStatusJSONEmptyDBIncludesZeroCounts(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)
	out := runStatusWithTestConfig(t, cfgPath, true, false)

	var decoded statusJSONOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	expectedKeys := []string{
		"queued",
		"planning",
		"implementing",
		"reviewing",
		"testing",
		"needs_pr",
		"failed",
		"cancelled",
		"pr_created",
		"merged",
		"rejected",
	}
	for _, key := range expectedKeys {
		got, ok := decoded.JobCounts[key]
		if !ok {
			t.Fatalf("missing key %q in job_counts", key)
		}
		if got != 0 {
			t.Fatalf("expected %q == 0, got %d", key, got)
		}
	}
}

func TestRunStatusWatchFlagsParseAndValidate(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeStatusConfig(t, tmp)

	prevStatusWatch := statusWatch
	prevStatusInterval := statusInterval
	t.Cleanup(func() {
		statusWatch = prevStatusWatch
		statusInterval = prevStatusInterval
	})

	cmd := &cobra.Command{}
	cmd.Flags().BoolVar(&statusShort, "short", false, "")
	cmd.Flags().BoolVar(&statusWatch, "watch", false, "")
	cmd.Flags().DurationVar(&statusInterval, "interval", defaultWatchInterval, "")

	if err := cmd.ParseFlags([]string{"--watch", "--interval", "25ms"}); err != nil {
		t.Fatalf("parse status watch flags: %v", err)
	}
	if !statusWatch {
		t.Fatalf("expected watch=true")
	}
	if statusInterval != 25*time.Millisecond {
		t.Fatalf("expected interval=25ms, got %v", statusInterval)
	}

	cmd = &cobra.Command{}
	cmd.Flags().BoolVar(&statusWatch, "watch", false, "")
	cmd.Flags().DurationVar(&statusInterval, "interval", defaultWatchInterval, "")
	if err := cmd.ParseFlags([]string{}); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if statusInterval != defaultWatchInterval {
		t.Fatalf("expected default interval %v, got %v", defaultWatchInterval, statusInterval)
	}

	_, err := runStatusWithTestConfigResult(t, cfgPath, context.Background(), true, false, "--watch", "--interval", "0s")
	if err == nil {
		t.Fatalf("expected invalid interval error")
	}
	_, err = runStatusWithTestConfigResult(t, cfgPath, context.Background(), true, false, "--watch", "--interval", "-1s")
	if err == nil {
		t.Fatalf("expected negative interval error")
	}
}

func TestRunStatusWatchJSONEmitsMultipleSnapshots(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "autopr.db")
	cfgPath := writeStatusConfig(t, tmp)
	seedStatusJobs(t, dbPath, []statusSeed{{state: "queued", count: 1}})

	go func() {
		time.AfterFunc(15*time.Millisecond, func() {
			mutateStore, err := db.Open(dbPath)
			if err != nil {
				return
			}
			defer mutateStore.Close()
			ctx := context.Background()
			issueID, err := mutateStore.UpsertIssue(ctx, db.IssueUpsert{
				ProjectName:   "project",
				Source:        "github",
				SourceIssueID: "issue-2",
				Title:         "issue 2",
				URL:           "https://example.com/2",
				State:         "open",
			})
			if err != nil {
				return
			}
			_, err = mutateStore.CreateJob(ctx, issueID, "project", 3)
			if err != nil {
				return
			}
		})
	}()

	runCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	out, err := runStatusWithTestConfigResult(t, cfgPath, runCtx, true, false, "--watch", "--interval", "20ms")
	if err != nil {
		t.Fatalf("run status watch: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple snapshots, got %d lines: %q", len(lines), out)
	}

	seenQueuedOne := false
	seenQueuedTwo := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("decode raw snapshot: %v", err)
		}
		if _, ok := raw["running"]; !ok {
			t.Fatalf("missing \"running\" field: %q", line)
		}
		if _, ok := raw["pid"]; !ok {
			t.Fatalf("missing \"pid\" field: %q", line)
		}
		if _, ok := raw["job_counts"]; !ok {
			t.Fatalf("missing \"job_counts\" field: %q", line)
		}
		var decoded statusJSONOutput
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if decoded.JobCounts["queued"] == 1 {
			seenQueuedOne = true
		}
		if decoded.JobCounts["queued"] == 2 {
			seenQueuedTwo = true
		}
	}
	if !seenQueuedOne {
		t.Fatalf("expected snapshot with one queued job")
	}
	if !seenQueuedTwo {
		t.Fatalf("expected snapshot with two queued/active jobs after mutation")
	}
}

func writeStatusConfig(t *testing.T, dir string) string {
	t.Helper()
	return writeStatusConfigWithPID(t, dir, filepath.Join(dir, "autopr.pid"))
}

func writeStatusConfigWithPID(t *testing.T, dir, pidPath string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "autopr.toml")
	dbPath := filepath.Join(dir, "autopr.db")
	cfg := fmt.Sprintf(`db_path = %q

[daemon]
pid_file = %q

[[projects]]
name = "project"
repo_url = "https://github.com/autopr/placeholder"
test_cmd = "echo ok"

[projects.github]
owner = "autopr"
repo = "placeholder"
`, dbPath, pidPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func seedStatusJobs(t *testing.T, dbPath string, seeds []statusSeed) {
	t.Helper()
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jobID := 0
	for _, seed := range seeds {
		for i := 0; i < seed.count; i++ {
			jobID++
			issueID, err := store.UpsertIssue(ctx, db.IssueUpsert{
				ProjectName:   "project",
				Source:        "github",
				SourceIssueID: fmt.Sprintf("issue-%d", jobID),
				Title:         fmt.Sprintf("issue %d", jobID),
				URL:           fmt.Sprintf("https://example.com/%d", jobID),
				State:         "open",
			})
			if err != nil {
				t.Fatalf("upsert issue: %v", err)
			}
			job, err := store.CreateJob(ctx, issueID, "project", 3)
			if err != nil {
				t.Fatalf("create job: %v", err)
			}
			if seed.state != "queued" {
				if _, err := store.Writer.ExecContext(ctx, `UPDATE jobs SET state = ? WHERE id = ?`, seed.state, job); err != nil {
					t.Fatalf("update job state: %v", err)
				}
			}
			if seed.state == "approved" && i < seed.merged {
				if _, err := store.Writer.ExecContext(ctx, `UPDATE jobs SET pr_merged_at = '2024-01-01T00:00:00Z' WHERE id = ?`, job); err != nil {
					t.Fatalf("mark merged job: %v", err)
				}
			}
		}
	}
}

func runStatusWithTestConfig(t *testing.T, configPath string, asJSON bool, asShort bool) string {
	out, err := runStatusWithTestConfigResult(t, configPath, context.Background(), asJSON, asShort)
	if err != nil {
		t.Fatalf("run status: %v", err)
	}
	return out
}

func runStatusWithTestConfigResult(t *testing.T, configPath string, ctx context.Context, asJSON bool, asShort bool, args ...string) (string, error) {
	t.Helper()
	prevCfgPath := cfgPath
	prevJSON := jsonOut
	prevShort := statusShort
	prevWatch := statusWatch
	prevInterval := statusInterval
	cfgPath = configPath
	jsonOut = asJSON
	statusShort = asShort
	statusWatch = false
	statusInterval = defaultWatchInterval
	t.Cleanup(func() {
		cfgPath = prevCfgPath
		jsonOut = prevJSON
		statusShort = prevShort
		statusWatch = prevWatch
		statusInterval = prevInterval
	})

	cmd := &cobra.Command{}
	cmd.Flags().BoolVar(&statusShort, "short", false, "print one-line status summary")
	cmd.Flags().BoolVar(&statusWatch, "watch", false, "refresh output periodically")
	cmd.Flags().DurationVar(&statusInterval, "interval", defaultWatchInterval, "refresh interval")
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		return "", err
	}
	cmd.SetContext(ctx)
	out, err := captureStdoutWithError(t, func() error {
		return runStatus(cmd, nil)
	})
	return out, err
}

func captureStdoutWithError(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	prevStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	os.Stdout = prevStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close read pipe: %v", err)
	}
	return string(out), runErr
}

func captureStdout(t *testing.T, fn func() error) string {
	out, err := captureStdoutWithError(t, fn)
	if err != nil {
		t.Fatalf("run status: %v", err)
	}
	return out
}
