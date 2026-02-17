package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CloneForJob creates a regular (non-bare) clone from the local bare repo
// and checks out a new branch from the base branch. This is used instead of
// git worktrees because LLM tools (e.g. codex) may run `git init` in the
// working directory, which destroys worktree .git link files but is a no-op
// on a regular .git directory.
func CloneForJob(ctx context.Context, bareRepoPath, destPath, branchName, baseBranch string) error {
	absRepo, err := filepath.Abs(bareRepoPath)
	if err != nil {
		return fmt.Errorf("abs repo path: %w", err)
	}

	// Clone from the local bare repo (uses hard links, very fast).
	if err := runGit(ctx, "", "clone", absRepo, destPath); err != nil {
		return fmt.Errorf("clone for job: %w", err)
	}

	// Create and checkout the job branch from the base branch.
	if err := runGit(ctx, destPath, "checkout", "-b", branchName, "origin/"+baseBranch); err != nil {
		return fmt.Errorf("create job branch: %w", err)
	}

	return nil
}

// RemoveJobDir removes a job's cloned working directory.
func RemoveJobDir(worktreePath string) {
	_ = os.RemoveAll(worktreePath)
}
