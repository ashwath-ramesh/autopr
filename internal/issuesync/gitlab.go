package issuesync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"fixflow/internal/config"
	"fixflow/internal/db"
)

func (s *Syncer) syncGitLab(ctx context.Context, p *config.ProjectConfig) error {
	if s.cfg.Tokens.GitLab == "" {
		slog.Debug("sync: skipping gitlab (no token)", "project", p.Name)
		return nil
	}

	baseURL := p.GitLab.BaseURL
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	projectID := p.GitLab.ProjectID

	// Get cursor (last updated_after timestamp).
	cursor, err := s.store.GetCursor(ctx, p.Name, "gitlab")
	if err != nil {
		return err
	}

	params := url.Values{
		"state":    {"opened"},
		"per_page": {"100"},
		"order_by": {"updated_at"},
		"sort":     {"asc"},
	}
	if cursor != "" {
		params.Set("updated_after", cursor)
	}

	apiURL := fmt.Sprintf("%s/api/v4/projects/%s/issues?%s", baseURL, projectID, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", s.cfg.Tokens.GitLab)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch gitlab issues: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("gitlab API %d: %s", resp.StatusCode, string(body))
	}

	var issues []gitlabIssue
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return fmt.Errorf("decode gitlab issues: %w", err)
	}

	slog.Debug("sync: gitlab issues fetched", "project", p.Name, "count", len(issues))

	var latestUpdated string
	for _, issue := range issues {
		// Skip issues created by fixflow (contain our marker).
		if containsMarker(issue.Description) {
			continue
		}

		labels := make([]string, 0, len(issue.Labels))
		labels = append(labels, issue.Labels...)

		ffid, err := s.store.UpsertIssue(ctx, db.IssueUpsert{
			ProjectName:   p.Name,
			Source:        "gitlab",
			SourceIssueID: fmt.Sprintf("%d", issue.IID),
			Title:         issue.Title,
			Body:          issue.Description,
			URL:           issue.WebURL,
			State:         "open",
			Labels:        labels,
			SourceUpdated: issue.UpdatedAt,
		})
		if err != nil {
			slog.Error("sync: upsert gitlab issue", "iid", issue.IID, "err", err)
			continue
		}

		s.createJobIfNeeded(ctx, ffid, p.Name)
		latestUpdated = issue.UpdatedAt
	}

	// Update cursor.
	if latestUpdated != "" {
		if err := s.store.SetCursor(ctx, p.Name, "gitlab", latestUpdated); err != nil {
			slog.Error("sync: set gitlab cursor", "err", err)
		}
	}

	return nil
}

type gitlabIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	WebURL      string   `json:"web_url"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	UpdatedAt   string   `json:"updated_at"`
	CreatedAt   string   `json:"created_at"`
}

func containsMarker(s string) bool {
	return strings.Contains(s, "ff-id:") || strings.Contains(s, "ff-sentry-issue:")
}
