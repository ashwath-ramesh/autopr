package issuesync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"fixflow/internal/config"
	"fixflow/internal/db"
)

func (s *Syncer) syncSentry(ctx context.Context, p *config.ProjectConfig) error {
	if s.cfg.Tokens.Sentry == "" {
		slog.Debug("sync: skipping sentry (no token)", "project", p.Name)
		return nil
	}

	org := p.Sentry.Org
	project := p.Sentry.Project
	baseURL := s.cfg.Sentry.BaseURL

	apiURL := fmt.Sprintf("%s/api/0/projects/%s/%s/issues/?query=is:unresolved&sort=date", baseURL, org, project)

	// Get cursor for pagination.
	cursor, err := s.store.GetCursor(ctx, p.Name, "sentry")
	if err != nil {
		return err
	}
	if cursor != "" {
		apiURL += "&cursor=" + cursor
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Tokens.Sentry)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch sentry issues: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("sentry API %d: %s", resp.StatusCode, string(body))
	}

	var issues []sentryIssue
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return fmt.Errorf("decode sentry issues: %w", err)
	}

	slog.Debug("sync: sentry issues fetched", "project", p.Name, "count", len(issues))

	for _, issue := range issues {
		body := fmt.Sprintf("Sentry Issue: %s\n\nCulprit: %s\nCount: %d\nFirst Seen: %s\nLast Seen: %s\n\nPermalink: %s",
			issue.Title, issue.Culprit, issue.Count, issue.FirstSeen, issue.LastSeen, issue.Permalink)

		ffid, err := s.store.UpsertIssue(ctx, db.IssueUpsert{
			ProjectName:   p.Name,
			Source:        "sentry",
			SourceIssueID: issue.ID,
			Title:         issue.Title,
			Body:          body,
			URL:           issue.Permalink,
			State:         "open",
			SourceUpdated: issue.LastSeen,
		})
		if err != nil {
			slog.Error("sync: upsert sentry issue", "id", issue.ID, "err", err)
			continue
		}

		s.createJobIfNeeded(ctx, ffid, p.Name)
	}

	// Update cursor from Link header if available.
	if nextCursor := parseSentryNextCursor(resp.Header.Get("Link")); nextCursor != "" {
		if err := s.store.SetCursor(ctx, p.Name, "sentry", nextCursor); err != nil {
			slog.Error("sync: set sentry cursor", "err", err)
		}
	}

	return nil
}

type sentryIssue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Culprit   string `json:"culprit"`
	Permalink string `json:"permalink"`
	Count     int    `json:"count,string"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
}

// parseSentryNextCursor extracts the next cursor from Sentry's Link header.
func parseSentryNextCursor(link string) string {
	// Sentry Link header format:
	// <url>; rel="previous"; results="false"; cursor="...", <url>; rel="next"; results="true"; cursor="..."
	for _, part := range splitLink(link) {
		if strings.Contains(part, `rel="next"`) && strings.Contains(part, `results="true"`) {
			return extractCursor(part)
		}
	}
	return ""
}

func splitLink(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '>' && i+1 < len(s) && s[i+1] == ',' {
			parts = append(parts, s[start:i+1])
			start = i + 2
			for start < len(s) && s[start] == ' ' {
				start++
			}
			i = start - 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func extractCursor(s string) string {
	prefix := `cursor="`
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(prefix)
	end := strings.Index(s[start:], `"`)
	if end < 0 {
		return s[start:]
	}
	return s[start : start+end]
}
