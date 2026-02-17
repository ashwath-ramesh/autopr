package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"autopr/internal/config"
	"autopr/internal/db"
)

func TestHealthEndpointSuccessFields(t *testing.T) {
	t.Parallel()
	server, store := newHealthTestServer(t)
	defer store.Close()

	res := performHealthRequest(t, server)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content type application/json, got %q", got)
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := payload["status"]; !ok {
		t.Fatalf("missing status field")
	}
	if _, ok := payload["uptime_seconds"]; !ok {
		t.Fatalf("missing uptime_seconds field")
	}
	if _, ok := payload["job_queue_depth"]; !ok {
		t.Fatalf("missing job_queue_depth field")
	}
}

func TestHealthQueueDepthCountsQueuedOnly(t *testing.T) {
	t.Parallel()
	server, store := newHealthTestServer(t)
	defer store.Close()

	seedJobWithState(t, store, 1, "queued")
	seedJobWithState(t, store, 2, "queued")
	seedJobWithState(t, store, 3, "planning")
	seedJobWithState(t, store, 4, "failed")

	res := performHealthRequest(t, server)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	jobQueueDepth, ok := payload["job_queue_depth"].(float64)
	if !ok {
		t.Fatalf("job_queue_depth should be number, got %T", payload["job_queue_depth"])
	}
	if int(jobQueueDepth) != 2 {
		t.Fatalf("expected job_queue_depth 2, got %v", payload["job_queue_depth"])
	}
}

func TestHealthUptimeSecondsNonNegativeInteger(t *testing.T) {
	t.Parallel()
	server, store := newHealthTestServer(t)
	defer store.Close()

	res := performHealthRequest(t, server)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	uptime, ok := payload["uptime_seconds"].(float64)
	if !ok {
		t.Fatalf("uptime_seconds should be number, got %T", payload["uptime_seconds"])
	}
	if uptime < 0 {
		t.Fatalf("uptime_seconds must be non-negative, got %v", uptime)
	}
	if math.Trunc(uptime) != uptime {
		t.Fatalf("uptime_seconds must be integer, got %v", uptime)
	}
}

func TestHealthQueueDepthQueryFailureReturns500JSON(t *testing.T) {
	t.Parallel()
	server, store := newHealthTestServer(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	res := performHealthRequest(t, server)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content type application/json, got %q", got)
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["error"] != "internal error" {
		t.Fatalf("expected internal error payload, got %v", payload)
	}
}

func newHealthTestServer(t *testing.T) (*Server, *db.Store) {
	t.Helper()

	store, err := db.Open(filepath.Join(t.TempDir(), "autopr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return NewServer(&config.Config{}, store, make(chan string, 1)), store
}

func performHealthRequest(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	return res
}

func seedJobWithState(t *testing.T, store *db.Store, n int, state string) {
	t.Helper()
	ctx := context.Background()
	issueID, err := store.UpsertIssue(ctx, db.IssueUpsert{
		ProjectName:   "proj",
		Source:        "gitlab",
		SourceIssueID: fmt.Sprintf("%d", n),
		Title:         fmt.Sprintf("issue-%d", n),
		URL:           fmt.Sprintf("https://example.com/%d", n),
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert issue %d: %v", n, err)
	}
	jobID, err := store.CreateJob(ctx, issueID, "proj", 3)
	if err != nil {
		t.Fatalf("create job %d: %v", n, err)
	}
	if state == "queued" {
		return
	}
	if _, err := store.Writer.ExecContext(ctx, `UPDATE jobs SET state = ? WHERE id = ?`, state, jobID); err != nil {
		t.Fatalf("set job state %d: %v", n, err)
	}
}
