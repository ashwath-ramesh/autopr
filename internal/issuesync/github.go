package issuesync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"fixflow/internal/config"
	"fixflow/internal/db"
)

func (s *Syncer) syncGitHub(ctx context.Context, p *config.ProjectConfig) error {
	if s.cfg.Tokens.GitHub == "" {
		slog.Debug("sync: skipping github (no token)", "project", p.Name)
		return nil
	}

	owner := p.GitHub.Owner
	repo := p.GitHub.Repo

	// Get cursor (last updated since timestamp).
	cursor, err := s.store.GetCursor(ctx, p.Name, "github")
	if err != nil {
		return err
	}

	params := url.Values{
		"state":    {"open"},
		"per_page": {"100"},
		"sort":     {"updated"},
		"direction": {"asc"},
	}
	if cursor != "" {
		params.Set("since", cursor)
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?%s", owner, repo, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Tokens.GitHub)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch github issues: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("github API %d: %s", resp.StatusCode, string(body))
	}

	var issues []githubIssue
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return fmt.Errorf("decode github issues: %w", err)
	}

	slog.Debug("sync: github issues fetched", "project", p.Name, "count", len(issues))

	var latestUpdated string
	for _, issue := range issues {
		// Skip pull requests (they show up in issues API).
		if issue.PullRequest != nil {
			continue
		}

		// Skip self-created issues.
		if containsMarker(issue.Body) {
			continue
		}

		labels := make([]string, 0, len(issue.Labels))
		for _, l := range issue.Labels {
			labels = append(labels, l.Name)
		}

		ffid, err := s.store.UpsertIssue(ctx, db.IssueUpsert{
			ProjectName:   p.Name,
			Source:        "github",
			SourceIssueID: fmt.Sprintf("%d", issue.Number),
			Title:         issue.Title,
			Body:          issue.Body,
			URL:           issue.HTMLURL,
			State:         "open",
			Labels:        labels,
			SourceUpdated: issue.UpdatedAt,
		})
		if err != nil {
			slog.Error("sync: upsert github issue", "number", issue.Number, "err", err)
			continue
		}

		s.createJobIfNeeded(ctx, ffid, p.Name)
		latestUpdated = issue.UpdatedAt
	}

	if latestUpdated != "" {
		if err := s.store.SetCursor(ctx, p.Name, "github", latestUpdated); err != nil {
			slog.Error("sync: set github cursor", "err", err)
		}
	}

	return nil
}

type githubIssue struct {
	Number      int           `json:"number"`
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	State       string        `json:"state"`
	Labels      []githubLabel `json:"labels"`
	UpdatedAt   string        `json:"updated_at"`
	PullRequest *struct{}     `json:"pull_request,omitempty"`
}

type githubLabel struct {
	Name string `json:"name"`
}
