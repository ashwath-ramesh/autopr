package git

import (
	"context"
	"os"
	"sync"
)

// repoMutexes provides per-repo locking for worktree operations.
var (
	repoMu      sync.Mutex
	repoMutexes = map[string]*sync.Mutex{}
)

func lockRepo(repoPath string) *sync.Mutex {
	repoMu.Lock()
	defer repoMu.Unlock()
	mu, ok := repoMutexes[repoPath]
	if !ok {
		mu = &sync.Mutex{}
		repoMutexes[repoPath] = mu
	}
	return mu
}

// CreateWorktree adds a new git worktree for the given branch.
func CreateWorktree(ctx context.Context, repoPath, worktreePath, branchName string) error {
	mu := lockRepo(repoPath)
	mu.Lock()
	defer mu.Unlock()
	return runGit(ctx, repoPath, "worktree", "add", worktreePath, branchName)
}

// RemoveWorktree removes a git worktree.
func RemoveWorktree(ctx context.Context, repoPath, worktreePath string) error {
	mu := lockRepo(repoPath)
	mu.Lock()
	defer mu.Unlock()
	// Remove the directory first if git worktree remove fails.
	err := runGit(ctx, repoPath, "worktree", "remove", "--force", worktreePath)
	if err != nil {
		// Fallback: remove dir and prune.
		_ = os.RemoveAll(worktreePath)
		_ = runGit(ctx, repoPath, "worktree", "prune")
	}
	return nil
}
