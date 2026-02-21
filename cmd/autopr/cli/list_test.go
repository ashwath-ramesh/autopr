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

	"autopr/internal/db"

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

	jobs := decodeListJobs(t, out)
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs in JSON output, got %d", len(jobs))
	}
}

func TestRunListSummaryLineShowsBuckets(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 3},
		{state: "planning", count: 2},
		{state: "failed", count: 1},
		{state: "rejected", count: 2},
		{state: "cancelled", count: 1},
		{state: "approved", count: 3, merged: 3},
	})

	out := runListWithTestConfigWithOptions(t, cfg, false, "", "all", "updated_at", false, false)
	got := strings.TrimSpace(out)
	want := "Total: 12 jobs (3 queued, 2 active, 4 failed, 3 merged)"
	if !strings.Contains(got, want) {
		t.Fatalf("expected summary line %q in output: %q", want, got)
	}
}

func TestRunListSummaryRespectsStateFilter(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 3},
		{state: "planning", count: 2},
		{state: "failed", count: 1},
		{state: "approved", count: 1, merged: 0},
	})

	out := runListWithTestConfigWithOptions(t, cfg, false, "", "active", "updated_at", false, false)
	got := strings.TrimSpace(out)
	want := "Total: 2 jobs (0 queued, 2 active, 0 failed, 0 merged)"
	if !strings.Contains(got, want) {
		t.Fatalf("expected summary line %q in output: %q", want, got)
	}
}

func TestRunListSummaryNotPrintedInJSON(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	seedStatusJobs(t, dbPath, []statusSeed{
		{state: "queued", count: 1},
	})

	out := runListWithTestConfig(t, cfg, true)
	got := strings.TrimSpace(out)
	if strings.Contains(got, "Total:") {
		t.Fatalf("unexpected summary line in JSON output: %q", got)
	}

	jobs := decodeListJobs(t, out)
	if len(jobs) != 1 {
		t.Fatalf("expected one job in JSON output, got %d", len(jobs))
	}
}

func TestNormalizeListSort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "updated_at", want: "updated_at"},
		{in: "created_at", want: "created_at"},
		{in: "state", want: "state"},
		{in: "project", want: "project"},
	} {
		got, err := normalizeListSort(tc.in)
		if err != nil {
			t.Fatalf("normalizeListSort(%q): unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeListSort(%q): expected %q, got %q", tc.in, tc.want, got)
		}
	}
}

func TestNormalizeListSortRejectsInvalidInput(t *testing.T) {
	_, err := normalizeListSort("bad")
	if err == nil {
		t.Fatalf("expected error for invalid sort value")
	}
	if !strings.Contains(err.Error(), "expected one of") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeListState(t *testing.T) {
	for _, state := range []string{"all", "active", "merged", "queued", "planning", "implementing", "reviewing", "testing", "ready", "rebasing", "resolving", "resolving_conflicts", "awaiting_checks", "approved", "rejected", "failed", "cancelled"} {
		got, err := normalizeListState(state)
		if err != nil {
			t.Fatalf("normalizeListState(%q): unexpected error: %v", state, err)
		}
		if state == "resolving" && got != "resolving_conflicts" {
			t.Fatalf("normalizeListState(%q): expected resolving_conflicts, got %q", state, got)
		}
		if state != "resolving" && got != state {
			t.Fatalf("normalizeListState(%q): expected same value, got %q", state, got)
		}
	}
}

func TestNormalizeListStateRejectsInvalidInput(t *testing.T) {
	_, err := normalizeListState("bad")
	if err == nil {
		t.Fatalf("expected error for invalid state value")
	}
	if !strings.Contains(err.Error(), "expected one of") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunListSortByCreatedAtHonorsDirection(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	ids := createListJobsForTest(t, dbPath, []listJobSeed{
		{state: "queued", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
		{state: "queued", createdAt: "2025-01-03T00:00:00Z", updatedAt: "2025-01-03T00:00:00Z"},
		{state: "queued", createdAt: "2025-01-02T00:00:00Z", updatedAt: "2025-01-02T00:00:00Z"},
	})

	out := runListWithTestConfigWithOptions(t, cfg, true, "", "all", "created_at", false, false)
	desc := decodeListJobs(t, out)
	if got, want := jobIDs(desc), []string{ids[1], ids[2], ids[0]}; !slicesEqual(got, want) {
		t.Fatalf("created_at desc: expected %v, got %v", want, got)
	}

	out = runListWithTestConfigWithOptions(t, cfg, true, "", "all", "created_at", true, false)
	asc := decodeListJobs(t, out)
	if got, want := jobIDs(asc), []string{ids[0], ids[2], ids[1]}; !slicesEqual(got, want) {
		t.Fatalf("created_at asc: expected %v, got %v", want, got)
	}
}

func TestRunListSortByState(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	ids := createListJobsForTest(t, dbPath, []listJobSeed{
		{state: "failed", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
		{state: "planning", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
		{state: "queued", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
	})

	out := runListWithTestConfigWithOptions(t, cfg, true, "", "all", "state", true, false)
	jobs := decodeListJobs(t, out)
	if got, want := jobIDs(jobs), []string{ids[2], ids[1], ids[0]}; !slicesEqual(got, want) {
		t.Fatalf("state sort asc: expected %v, got %v", want, got)
	}
}

func TestRunListSortByProject(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	ids := createListJobsForTest(t, dbPath, []listJobSeed{
		{state: "queued", project: "zulu", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
		{state: "queued", project: "alpha", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
		{state: "queued", project: "bravo", createdAt: "2025-01-01T00:00:00Z", updatedAt: "2025-01-01T00:00:00Z"},
	})

	out := runListWithTestConfigWithOptions(t, cfg, true, "", "all", "project", true, false)
	jobs := decodeListJobs(t, out)
	if got, want := jobIDs(jobs), []string{ids[1], ids[2], ids[0]}; !slicesEqual(got, want) {
		t.Fatalf("project sort asc: expected %v, got %v", want, got)
	}
}

func TestRunListStateFiltersSpecialValues(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)
	dbPath := filepath.Join(tmp, "autopr.db")
	ids := createListJobsForTest(t, dbPath, []listJobSeed{
		{state: "planning", updatedAt: "2025-01-03T00:00:00Z"},
		{state: "queued", updatedAt: "2025-01-04T00:00:00Z"},
		{state: "approved", updatedAt: "2025-01-05T00:00:00Z", mergedAt: "2025-01-05T00:00:00Z"},
		{state: "rebasing", updatedAt: "2025-01-02T00:00:00Z"},
		{state: "resolving_conflicts", updatedAt: "2025-01-01T00:00:00Z"},
	})

	tests := []struct {
		name   string
		state  string
		wantID []string
	}{
		{name: "merged", state: "merged", wantID: []string{ids[2]}},
		{name: "active", state: "active", wantID: []string{ids[0], ids[3], ids[4]}},
		{name: "rebasing", state: "rebasing", wantID: []string{ids[3]}},
		{name: "resolving", state: "resolving", wantID: []string{ids[4]}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out := runListWithTestConfigWithOptions(t, cfg, true, "", tc.state, "updated_at", false, false)
			jobs := decodeListJobs(t, out)
			gotIDs := jobIDs(jobs)
			if !slicesEqual(gotIDs, tc.wantID) {
				t.Fatalf("state=%q: expected job IDs %v, got %v", tc.state, tc.wantID, gotIDs)
			}
		})
	}
}

func TestRunListInvalidSortReturnsError(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)

	_, err := runListWithTestConfigWithOptionsResult(t, cfg, true, "", "all", "bad", false, false)
	if err == nil {
		t.Fatalf("expected invalid sort error")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunListInvalidStateReturnsError(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)

	_, err := runListWithTestConfigWithOptionsResult(t, cfg, true, "", "bad", "updated_at", false, false)
	if err == nil {
		t.Fatalf("expected invalid state error")
	}
	if !strings.Contains(err.Error(), "invalid --state") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunListRejectsConflictingDirectionFlags(t *testing.T) {
	tmp := t.TempDir()
	cfg := writeStatusConfig(t, tmp)

	_, err := runListWithTestConfigWithOptionsResult(t, cfg, true, "", "all", "updated_at", true, true)
	if err == nil {
		t.Fatalf("expected conflicting direction flags error")
	}
	if !strings.Contains(err.Error(), "--asc") || !strings.Contains(err.Error(), "--desc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func runListWithTestConfig(t *testing.T, configPath string, asJSON bool) string {
	out, err := runListWithTestConfigWithOptionsResult(t, configPath, asJSON, "", "all", "updated_at", false, false)
	if err != nil {
		t.Fatalf("run list: %v", err)
	}
	return out
}

func runListWithTestConfigWithOptions(t *testing.T, configPath string, asJSON bool, project, state, sort string, asc, desc bool) string {
	out, err := runListWithTestConfigWithOptionsResult(t, configPath, asJSON, project, state, sort, asc, desc)
	if err != nil {
		t.Fatalf("run list: %v", err)
	}
	return out
}

func runListWithTestConfigWithOptionsResult(t *testing.T, configPath string, asJSON bool, project, state, sort string, asc, desc bool) (string, error) {
	t.Helper()
	prevCfgPath := cfgPath
	prevJSON := jsonOut
	prevProject := listProject
	prevState := listState
	prevSort := listSort
	prevAsc := listAsc
	prevDesc := listDesc
	cfgPath = configPath
	jsonOut = asJSON
	listProject = project
	listState = state
	listSort = sort
	listAsc = asc
	listDesc = desc
	t.Cleanup(func() {
		cfgPath = prevCfgPath
		jsonOut = prevJSON
		listProject = prevProject
		listState = prevState
		listSort = prevSort
		listAsc = prevAsc
		listDesc = prevDesc
	})

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return captureStdoutWithError(t, func() error {
		return runList(cmd, nil)
	})
}

type listJobSeed struct {
	state     string
	project   string
	createdAt string
	updatedAt string
	mergedAt  string
}

func createListJobsForTest(t *testing.T, dbPath string, seeds []listJobSeed) []string {
	t.Helper()
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	var ids []string
	for i, seed := range seeds {
		project := seed.project
		if project == "" {
			project = "project"
		}
		createdAt := seed.createdAt
		if createdAt == "" {
			createdAt = fmt.Sprintf("2025-01-0%dT00:00:00Z", i+1)
		}
		updatedAt := seed.updatedAt
		if updatedAt == "" {
			updatedAt = createdAt
		}

		issueID, err := store.UpsertIssue(ctx, db.IssueUpsert{
			ProjectName:   project,
			Source:        "github",
			SourceIssueID: fmt.Sprintf("issue-%d", i+1),
			Title:         fmt.Sprintf("issue-%d", i+1),
			URL:           fmt.Sprintf("https://example.com/%d", i+1),
			State:         "open",
		})
		if err != nil {
			t.Fatalf("upsert issue %d: %v", i+1, err)
		}
		jobID, err := store.CreateJob(ctx, issueID, project, 3)
		if err != nil {
			t.Fatalf("create job %d: %v", i+1, err)
		}
		if _, err := store.Writer.ExecContext(ctx, `UPDATE jobs SET state = ?, project_name = ?, created_at = ?, updated_at = ?, pr_merged_at = ? WHERE id = ?`, seed.state, project, createdAt, updatedAt, seed.mergedAt, jobID); err != nil {
			t.Fatalf("seed job %d: %v", i+1, err)
		}
		ids = append(ids, jobID)
	}
	return ids
}

func decodeListJobs(t *testing.T, out string) []db.Job {
	t.Helper()
	var jobs []db.Job
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &jobs); err != nil {
		t.Fatalf("decode JSON jobs: %v", err)
	}
	return jobs
}

func jobIDs(jobs []db.Job) []string {
	ids := make([]string, len(jobs))
	for i, job := range jobs {
		ids[i] = job.ID
	}
	return ids
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	return strings.TrimSpace(string(out)), runErr
}
