package issuesync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"autopr/internal/config"
	"autopr/internal/db"
)

// Syncer periodically pulls issues from configured sources.
type Syncer struct {
	cfg   *config.Config
	store *db.Store
	jobCh chan<- string
}

func NewSyncer(cfg *config.Config, store *db.Store, jobCh chan<- string) *Syncer {
	return &Syncer{cfg: cfg, store: store, jobCh: jobCh}
}

// RunLoop polls all configured sources at the given interval.
func (s *Syncer) RunLoop(ctx context.Context, interval time.Duration) {
	slog.Info("sync loop starting", "interval", interval)

	// Run immediately on start.
	s.syncAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Debug("sync loop stopping")
			return
		case <-ticker.C:
			s.syncAll(ctx)
		}
	}
}

func (s *Syncer) syncAll(ctx context.Context) {
	for i := range s.cfg.Projects {
		p := &s.cfg.Projects[i]
		if err := s.syncProject(ctx, p); err != nil {
			slog.Error("sync project failed", "project", p.Name, "err", err)
		}
	}
}

func (s *Syncer) syncProject(ctx context.Context, p *config.ProjectConfig) error {
	if p.GitLab != nil {
		if err := s.syncGitLab(ctx, p); err != nil {
			return fmt.Errorf("gitlab sync: %w", err)
		}
	}
	if p.GitHub != nil {
		if err := s.syncGitHub(ctx, p); err != nil {
			return fmt.Errorf("github sync: %w", err)
		}
	}
	if p.Sentry != nil {
		if err := s.syncSentry(ctx, p); err != nil {
			return fmt.Errorf("sentry sync: %w", err)
		}
	}
	return nil
}

// createJobIfNeeded creates a job for an issue if there isn't already an active one.
func (s *Syncer) createJobIfNeeded(ctx context.Context, ffid, projectName string) {
	active, err := s.store.HasActiveJobForIssue(ctx, ffid)
	if err != nil {
		slog.Error("sync: check active job", "err", err)
		return
	}
	if active {
		return
	}

	jobID, err := s.store.CreateJob(ctx, ffid, projectName, s.cfg.Daemon.MaxIterations)
	if err != nil {
		slog.Error("sync: create job", "err", err)
		return
	}

	select {
	case s.jobCh <- jobID:
	default:
		slog.Warn("sync: job channel full", "job_id", jobID)
	}

	slog.Info("sync: created job", "job_id", jobID, "ffid", ffid)
}
