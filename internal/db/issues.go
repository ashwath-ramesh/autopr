package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Issue struct {
	FixFlowIssueID string
	ProjectName    string
	Source         string
	SourceIssueID  string
	Title          string
	Body           string
	URL            string
	State          string
	LabelsJSON     string
	SourceMetaJSON string
	SourceUpdated  string
	SyncedAt       string
}

type IssueUpsert struct {
	ProjectName   string
	Source        string
	SourceIssueID string
	Title         string
	Body          string
	URL           string
	State         string
	Labels        []string
	SourceMeta    map[string]any
	SourceUpdated string
}

func (s *Store) UpsertIssue(ctx context.Context, in IssueUpsert) (string, error) {
	newID, err := newFixFlowIssueID()
	if err != nil {
		return "", err
	}
	now := nowRFC3339()
	if in.SourceUpdated == "" {
		in.SourceUpdated = now
	}
	labelsJSON := "[]"
	if len(in.Labels) > 0 {
		b, _ := json.Marshal(in.Labels)
		labelsJSON = string(b)
	}
	metaJSON := "{}"
	if len(in.SourceMeta) > 0 {
		b, _ := json.Marshal(in.SourceMeta)
		metaJSON = string(b)
	}
	const q = `
INSERT INTO issues(
  fixflow_issue_id, project_name, source, source_issue_id, title, body, url, state,
  labels_json, source_meta_json, source_updated_at, synced_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_name, source, source_issue_id) DO UPDATE SET
  title=excluded.title,
  body=excluded.body,
  url=excluded.url,
  state=excluded.state,
  labels_json=excluded.labels_json,
  source_meta_json=excluded.source_meta_json,
  source_updated_at=excluded.source_updated_at,
  synced_at=excluded.synced_at
RETURNING fixflow_issue_id`
	var actualID string
	err = s.Writer.QueryRowContext(ctx, q,
		newID, in.ProjectName, in.Source, in.SourceIssueID, in.Title, in.Body, in.URL, in.State,
		labelsJSON, metaJSON, in.SourceUpdated, now,
	).Scan(&actualID)
	if err != nil {
		return "", fmt.Errorf("upsert issue %s/%s/%s: %w", in.ProjectName, in.Source, in.SourceIssueID, err)
	}
	return actualID, nil
}

func (s *Store) GetIssueByFFID(ctx context.Context, fixflowID string) (Issue, error) {
	const q = `
SELECT fixflow_issue_id, project_name, source, source_issue_id, title, body, url, state,
       labels_json, source_meta_json, source_updated_at, synced_at
FROM issues WHERE fixflow_issue_id = ?`
	var it Issue
	err := s.Reader.QueryRowContext(ctx, q, fixflowID).Scan(
		&it.FixFlowIssueID, &it.ProjectName, &it.Source, &it.SourceIssueID,
		&it.Title, &it.Body, &it.URL, &it.State,
		&it.LabelsJSON, &it.SourceMetaJSON, &it.SourceUpdated, &it.SyncedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return Issue{}, fmt.Errorf("issue %s not found", fixflowID)
		}
		return Issue{}, fmt.Errorf("get issue %s: %w", fixflowID, err)
	}
	return it, nil
}

// Cursor operations.

func (s *Store) GetCursor(ctx context.Context, project, source string) (string, error) {
	const q = `SELECT cursor_value FROM sync_cursors WHERE project_name = ? AND source = ?`
	var v sql.NullString
	err := s.Reader.QueryRowContext(ctx, q, project, source).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("get cursor %s/%s: %w", project, source, err)
	}
	return v.String, nil
}

func (s *Store) SetCursor(ctx context.Context, project, source, cursor string) error {
	const q = `
INSERT INTO sync_cursors(project_name, source, cursor_value, last_synced_at)
VALUES(?,?,?,?)
ON CONFLICT(project_name, source) DO UPDATE SET
  cursor_value=excluded.cursor_value,
  last_synced_at=excluded.last_synced_at`
	_, err := s.Writer.ExecContext(ctx, q, project, source, cursor, nowRFC3339())
	if err != nil {
		return fmt.Errorf("set cursor %s/%s: %w", project, source, err)
	}
	return nil
}

// Helpers.

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newFixFlowIssueID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate fixflow_issue_id: %w", err)
	}
	return "ff-" + strings.ToLower(hex.EncodeToString(buf)), nil
}
