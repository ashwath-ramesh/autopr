package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunListNoJobsShowsStartHint(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)

	out := runListWithTestConfig(t, cfg, false)
	got := strings.TrimSpace(out)
	want := "No jobs found. Run 'ap start' to begin processing issues."
	if got != want {
		t.Fatalf("unexpected output: got %q, want %q", got, want)
	}
}

func TestRunListNoJobsJSONStillWorks(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)

	out := runListWithTestConfig(t, cfg, true)
	got := strings.TrimSpace(out)
	if strings.Contains(got, "No jobs found.") {
		t.Fatalf("unexpected human-readable message in JSON output: %q", got)
	}

	var jobs []map[string]any
	if err := json.Unmarshal([]byte(got), &jobs); err != nil {
		t.Fatalf("decode JSON jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs in JSON output, got %d", len(jobs))
	}
}

func runListWithTestConfig(t *testing.T, configPath string, asJSON bool) string {
	t.Helper()
	prevCfgPath := cfgPath
	prevJSON := jsonOut
	cfgPath = configPath
	jsonOut = asJSON
	t.Cleanup(func() {
		cfgPath = prevCfgPath
		jsonOut = prevJSON
	})

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return captureStdout(t, func() error {
		return runList(cmd, nil)
	})
}
