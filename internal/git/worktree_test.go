package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneForJob_RemovesStaleDestinationAndSucceeds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	remote := createRemoteWithMainBranch(t, tmp)

	destPath := filepath.Join(tmp, "repos", "worktrees", "ap-job-123")
	if err := os.MkdirAll(destPath, 0o755); err != nil {
		t.Fatalf("mkdir stale destination: %v", err)
	}
	marker := filepath.Join(destPath, "stale-marker.txt")
	if err := os.WriteFile(marker, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}

	branchName := "autopr/job-123"
	if err := CloneForJob(ctx, remote, "", destPath, branchName, "main"); err != nil {
		t.Fatalf("clone for job: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected stale marker to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(destPath, ".git")); err != nil {
		t.Fatalf("expected cloned repo at destination: %v", err)
	}

	currentBranch, err := runGitOutput(ctx, destPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("read current branch: %v", err)
	}
	if got := strings.TrimSpace(currentBranch); got != branchName {
		t.Fatalf("expected branch %q, got %q", branchName, got)
	}
}

func TestCloneForJob_CreatesMissingParentDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	remote := createRemoteWithMainBranch(t, tmp)

	destPath := filepath.Join(tmp, "missing", "nested", "worktrees", "ap-job-456")
	if err := CloneForJob(ctx, remote, "", destPath, "autopr/job-456", "main"); err != nil {
		t.Fatalf("clone for job: %v", err)
	}

	if _, err := os.Stat(filepath.Join(destPath, ".git")); err != nil {
		t.Fatalf("expected destination to be cloned: %v", err)
	}
}

func TestCloneForJob_RejectsUnsafeDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		destPath        string
		wantErrContains string
	}{
		{name: "empty", destPath: "", wantErrContains: "empty"},
		{name: "dot", destPath: ".", wantErrContains: "unsafe"},
		{name: "dot dot", destPath: "..", wantErrContains: "unsafe"},
		{name: "root", destPath: string(os.PathSeparator), wantErrContains: "unsafe"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CloneForJob(context.Background(), "https://example.com/repo.git", "", tc.destPath, "autopr/job", "main")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrContains, err)
			}
		})
	}
}

func createRemoteWithMainBranch(t *testing.T, tmp string) string {
	t.Helper()

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)

	seed := filepath.Join(tmp, "seed")
	runGitCmd(t, "", "init", seed)
	runGitCmd(t, seed, "config", "user.email", "test@example.com")
	runGitCmd(t, seed, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitCmd(t, seed, "add", "README.md")
	runGitCmd(t, seed, "commit", "-m", "init")
	runGitCmd(t, seed, "branch", "-M", "main")
	runGitCmd(t, seed, "remote", "add", "origin", remote)
	runGitCmd(t, seed, "push", "-u", "origin", "main")

	return remote
}
