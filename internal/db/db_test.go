package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertIssueAssignsAndPreservesFixFlowIssueID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	store, err := Open(filepath.Join(tmp, "fixflow.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	firstID, err := store.UpsertIssue(ctx, IssueUpsert{
		ProjectName:   "myproject",
		Source:        "sentry",
		SourceIssueID: "95751702",
		Title:         "boom",
		URL:           "https://sentry.local/issues/95751702",
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	if firstID == "" || !strings.HasPrefix(firstID, "ff-") {
		t.Fatalf("expected ff- prefixed id, got %q", firstID)
	}

	secondID, err := store.UpsertIssue(ctx, IssueUpsert{
		ProjectName:   "myproject",
		Source:        "sentry",
		SourceIssueID: "95751702",
		Title:         "boom updated",
		URL:           "https://sentry.local/issues/95751702",
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("expected stable fixflow id, first=%s second=%s", firstID, secondID)
	}

	it, err := store.GetIssueByFFID(ctx, firstID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if it.Title != "boom updated" {
		t.Fatalf("expected updated title, got %s", it.Title)
	}
}

func TestGetIssueByFFIDMissingReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	store, err := Open(filepath.Join(tmp, "fixflow.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	_, err = store.GetIssueByFFID(ctx, "missing")
	if err == nil {
		t.Fatalf("expected error for missing issue")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJobStateTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	store, err := Open(filepath.Join(tmp, "fixflow.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	// Create issue first.
	ffid, err := store.UpsertIssue(ctx, IssueUpsert{
		ProjectName:   "myproject",
		Source:        "gitlab",
		SourceIssueID: "1",
		Title:         "test issue",
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	// Create job.
	jobID, err := store.CreateJob(ctx, ffid, "myproject", 3)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if !strings.HasPrefix(jobID, "ff-job-") {
		t.Fatalf("expected ff-job- prefix, got %q", jobID)
	}

	// Claim job (queued -> planning).
	claimedID, err := store.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimedID != jobID {
		t.Fatalf("expected claimed job %s, got %s", jobID, claimedID)
	}

	// Verify state.
	job, err := store.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.State != "planning" {
		t.Fatalf("expected planning state, got %s", job.State)
	}

	// Valid transition: planning -> implementing.
	if err := store.TransitionState(ctx, jobID, "planning", "implementing"); err != nil {
		t.Fatalf("transition planning->implementing: %v", err)
	}

	// Invalid transition: implementing -> ready (should fail).
	if err := store.TransitionState(ctx, jobID, "implementing", "ready"); err == nil {
		t.Fatalf("expected error for invalid transition")
	}

	// Valid transition: implementing -> reviewing.
	if err := store.TransitionState(ctx, jobID, "implementing", "reviewing"); err != nil {
		t.Fatalf("transition implementing->reviewing: %v", err)
	}
}

func TestHasActiveJobForIssue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	store, err := Open(filepath.Join(tmp, "fixflow.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ffid, err := store.UpsertIssue(ctx, IssueUpsert{
		ProjectName:   "myproject",
		Source:        "gitlab",
		SourceIssueID: "2",
		Title:         "test",
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// No active job.
	active, err := store.HasActiveJobForIssue(ctx, ffid)
	if err != nil {
		t.Fatalf("check active: %v", err)
	}
	if active {
		t.Fatalf("expected no active job")
	}

	// Create job.
	_, err = store.CreateJob(ctx, ffid, "myproject", 3)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Now active.
	active, err = store.HasActiveJobForIssue(ctx, ffid)
	if err != nil {
		t.Fatalf("check active: %v", err)
	}
	if !active {
		t.Fatalf("expected active job")
	}
}

func TestRecoverInFlightJobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	store, err := Open(filepath.Join(tmp, "fixflow.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	ffid, err := store.UpsertIssue(ctx, IssueUpsert{
		ProjectName:   "myproject",
		Source:        "gitlab",
		SourceIssueID: "3",
		Title:         "crash test",
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	jobID, err := store.CreateJob(ctx, ffid, "myproject", 3)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Simulate in-flight: claim the job (queued -> planning).
	_, err = store.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Recover.
	n, err := store.RecoverInFlightJobs(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 recovered, got %d", n)
	}

	// Job should be back to queued.
	job, err := store.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if job.State != "queued" {
		t.Fatalf("expected queued, got %s", job.State)
	}
}
