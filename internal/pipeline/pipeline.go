package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"autopr/internal/config"
	"autopr/internal/db"
	"autopr/internal/git"
	"autopr/internal/llm"
)

// errReviewChangesRequested signals that code review requested changes.
var errReviewChangesRequested = errors.New("code review requested changes")

// errTestsFailed signals that tests failed and the job should retry from implementing.
var errTestsFailed = errors.New("tests failed")

// Runner orchestrates the full pipeline for a job.
type Runner struct {
	store    *db.Store
	provider llm.Provider
	cfg      *config.Config
}

func New(store *db.Store, provider llm.Provider, cfg *config.Config) *Runner {
	return &Runner{store: store, provider: provider, cfg: cfg}
}

// Run processes a job through the pipeline: plan -> implement <-> review -> tests -> ready.
func (r *Runner) Run(ctx context.Context, jobID string) error {
	job, err := r.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}

	issue, err := r.store.GetIssueByAPID(ctx, job.AutoPRIssueID)
	if err != nil {
		return fmt.Errorf("get issue for job %s: %w", jobID, err)
	}

	projectCfg, ok := r.cfg.ProjectByName(job.ProjectName)
	if !ok {
		return r.failJob(ctx, jobID, job.State, "project not found: "+job.ProjectName)
	}

	// Determine token for git operations.
	token := r.tokenForProject(projectCfg)

	// Clone repo directly for this job (regular clone, not a worktree).
	branchName := buildBranchName(issue, jobID)
	worktreePath := filepath.Join(r.cfg.ReposRoot, "worktrees", jobID)

	if job.WorktreePath == "" {
		if err := git.CloneForJob(ctx, projectCfg.RepoURL, token, worktreePath, branchName, projectCfg.BaseBranch); err != nil {
			return r.failJob(ctx, jobID, job.State, "clone for job: "+err.Error())
		}
		_ = r.store.UpdateJobField(ctx, jobID, "worktree_path", worktreePath)
		_ = r.store.UpdateJobField(ctx, jobID, "branch_name", branchName)
	} else {
		worktreePath = job.WorktreePath
		branchName = job.BranchName
	}

	// Run pipeline steps based on current state.
	if err := r.runSteps(ctx, jobID, job.State, issue, projectCfg, worktreePath); err != nil {
		return err
	}

	// Auto-create PR if configured.
	if r.cfg.Daemon.AutoPR {
		return r.maybeAutoPR(ctx, jobID, issue, projectCfg)
	}

	return nil
}

func (r *Runner) runSteps(ctx context.Context, jobID, currentState string, issue db.Issue, projectCfg *config.ProjectConfig, workDir string) error {
	steps := []struct {
		state string
		next  string
		run   func(ctx context.Context, jobID string, issue db.Issue, projectCfg *config.ProjectConfig, workDir string) error
	}{
		{"planning", "implementing", r.runPlan},
		{"implementing", "reviewing", r.runImplement},
		{"reviewing", "testing", r.runCodeReview},
		{"testing", "ready", r.runTests},
	}

	for _, step := range steps {
		if currentState != step.state {
			continue
		}

		slog.Info("running step", "job", jobID, "step", db.StepForState(step.state))

		if err := step.run(ctx, jobID, issue, projectCfg, workDir); err != nil {
			// Code review requested changes — loop back to implementing.
			if errors.Is(err, errReviewChangesRequested) {
				if err := r.store.TransitionState(ctx, jobID, "reviewing", "implementing"); err != nil {
					return err
				}
				return r.handleRetryLoop(ctx, jobID, issue, projectCfg, workDir)
			}
			// Tests failed — loop back to implementing so LLM can fix.
			if errors.Is(err, errTestsFailed) {
				slog.Info("tests failed, looping back to implement", "job", jobID)
				if err := r.store.TransitionState(ctx, jobID, "testing", "implementing"); err != nil {
					return err
				}
				return r.handleRetryLoop(ctx, jobID, issue, projectCfg, workDir)
			}
			return r.failJob(ctx, jobID, step.state, err.Error())
		}

		// Transition to next state.
		if err := r.store.TransitionState(ctx, jobID, step.state, step.next); err != nil {
			return err
		}
		currentState = step.next
	}

	return nil
}

func (r *Runner) handleRetryLoop(ctx context.Context, jobID string, issue db.Issue, projectCfg *config.ProjectConfig, workDir string) error {
	job, err := r.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}

	if job.Iteration >= job.MaxIterations {
		slog.Info("max iterations reached, moving to ready for human review", "job", jobID, "iterations", job.Iteration)
		_ = r.store.TransitionState(ctx, jobID, job.State, "ready")
		return nil
	}

	if err := r.store.IncrementIteration(ctx, jobID); err != nil {
		return err
	}

	// Re-run from implementing.
	return r.runSteps(ctx, jobID, "implementing", issue, projectCfg, workDir)
}

func (r *Runner) failJob(ctx context.Context, jobID, fromState, errMsg string) error {
	slog.Error("job failed", "job", jobID, "state", fromState, "error", errMsg)
	_ = r.store.TransitionState(ctx, jobID, fromState, "failed")
	_ = r.store.UpdateJobField(ctx, jobID, "error_message", errMsg)
	return fmt.Errorf("job %s failed in %s: %s", jobID, fromState, errMsg)
}

func (r *Runner) invokeProvider(ctx context.Context, jobID, step string, iteration int, workDir, prompt string) (llm.Response, error) {
	sessionID, err := r.store.CreateSession(ctx, jobID, step, iteration, r.provider.Name())
	if err != nil {
		return llm.Response{}, fmt.Errorf("create session: %w", err)
	}

	resp, runErr := r.provider.Run(ctx, workDir, prompt)

	status := "completed"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
	}

	_ = r.store.CompleteSession(ctx, sessionID, status, resp.Text, prompt, "", resp.JSONLPath, resp.CommitSHA, errMsg, resp.InputTokens, resp.OutputTokens, resp.DurationMS)

	if runErr != nil {
		return resp, runErr
	}
	return resp, nil
}

func (r *Runner) maybeAutoPR(ctx context.Context, jobID string, issue db.Issue, projectCfg *config.ProjectConfig) error {
	job, err := r.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.State != "ready" {
		return nil
	}

	slog.Info("auto_pr enabled, creating PR", "job", jobID)

	prTitle, prBody := BuildPRContent(ctx, r.store, job, issue)

	prURL, err := createPRForProject(ctx, r.cfg, projectCfg, job, prTitle, prBody)
	if err != nil {
		slog.Error("auto-PR creation failed", "job", jobID, "err", err)
		return fmt.Errorf("auto-create PR: %w", err)
	}

	if prURL != "" {
		_ = r.store.UpdateJobField(ctx, jobID, "pr_url", prURL)
	}

	if err := r.store.TransitionState(ctx, jobID, "ready", "approved"); err != nil {
		return err
	}

	slog.Info("auto-PR created", "job", jobID, "pr_url", prURL)
	return nil
}

// createPRForProject creates a GitHub PR or GitLab MR based on project config.
func createPRForProject(ctx context.Context, cfg *config.Config, proj *config.ProjectConfig, job db.Job, title, body string) (string, error) {
	if job.BranchName == "" {
		return "", fmt.Errorf("job has no branch name — was the branch pushed?")
	}

	switch {
	case proj.GitHub != nil:
		if cfg.Tokens.GitHub == "" {
			return "", fmt.Errorf("GITHUB_TOKEN required to create PR")
		}
		return git.CreateGitHubPR(ctx, cfg.Tokens.GitHub, proj.GitHub.Owner, proj.GitHub.Repo,
			job.BranchName, proj.BaseBranch, title, body, false)

	case proj.GitLab != nil:
		if cfg.Tokens.GitLab == "" {
			return "", fmt.Errorf("GITLAB_TOKEN required to create MR")
		}
		return git.CreateGitLabMR(ctx, cfg.Tokens.GitLab, proj.GitLab.BaseURL, proj.GitLab.ProjectID,
			job.BranchName, proj.BaseBranch, title, body)

	default:
		return "", fmt.Errorf("project %q has no GitHub or GitLab config for PR creation", proj.Name)
	}
}

// BuildPRContent assembles the PR title and body from job data and artifacts.
func BuildPRContent(ctx context.Context, store *db.Store, job db.Job, issue db.Issue) (string, string) {
	title := fmt.Sprintf("[AutoPR] %s", issue.Title)

	var body strings.Builder
	body.WriteString(fmt.Sprintf("Closes %s\n\n", issue.URL))
	body.WriteString(fmt.Sprintf("**Issue:** %s\n\n", issue.Title))

	if plan, err := store.GetLatestArtifact(ctx, job.ID, "plan"); err == nil {
		content := plan.Content
		if len(content) > 2000 {
			content = content[:2000] + "\n\n_(truncated)_"
		}
		body.WriteString("<details>\n<summary>Plan</summary>\n\n")
		body.WriteString(content)
		body.WriteString("\n</details>\n\n")
	}

	body.WriteString(fmt.Sprintf("_Generated by [AutoPR](https://github.com/ashwath-ramesh/autopr) from job `%s`_\n", db.ShortID(job.ID)))

	return title, body.String()
}

// buildBranchName creates a descriptive branch name from the issue.
// Example: autopr/github-42-fix-login-timeout
func buildBranchName(issue db.Issue, jobID string) string {
	// Start with source and issue number if available.
	prefix := "autopr/"
	if issue.Source != "" && issue.SourceIssueID != "" {
		prefix += issue.Source + "-" + issue.SourceIssueID + "-"
	}

	// Slugify the issue title.
	slug := slugify(issue.Title)
	if slug == "" {
		// Fallback to short job ID if title is empty.
		return "autopr/" + db.ShortID(jobID)
	}

	// Keep branch name reasonable length.
	name := prefix + slug
	if len(name) > 60 {
		name = name[:60]
		// Don't end on a hyphen.
		name = strings.TrimRight(name, "-")
	}
	return name
}

// slugify converts a string to a git-branch-safe slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == '.':
			b.WriteRune('-')
		}
		// skip everything else
	}
	// Collapse consecutive hyphens.
	result := b.String()
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}

func (r *Runner) tokenForProject(p *config.ProjectConfig) string {
	if p.GitLab != nil {
		return r.cfg.Tokens.GitLab
	}
	if p.GitHub != nil {
		return r.cfg.Tokens.GitHub
	}
	return ""
}
