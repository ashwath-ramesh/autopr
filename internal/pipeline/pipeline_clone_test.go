package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"autopr/internal/config"
	"autopr/internal/db"
	"autopr/internal/llm"
)

func TestRun_StaleWorktreeDirectoryIsReplacedBeforeClone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()

	store, err := db.Open(filepath.Join(tmp, "autopr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	remote := createBareRemoteWithMain(t, tmp)
	cfg := &config.Config{
		ReposRoot: filepath.Join(tmp, "repos"),
		LLM:       config.LLMConfig{Provider: "codex"},
		Projects: []config.ProjectConfig{{
			Name:       "myproject",
			RepoURL:    remote,
			BaseBranch: "main",
			TestCmd:    "true",
			GitHub:     &config.ProjectGitHub{Owner: "org", Repo: "repo"},
		}},
	}

	issueID, err := store.UpsertIssue(ctx, db.IssueUpsert{
		ProjectName:   "myproject",
		Source:        "github",
		SourceIssueID: "101",
		Title:         "stale clone path retry",
		Body:          "retry should clean stale worktree",
		URL:           "https://github.com/org/repo/issues/101",
		State:         "open",
	})
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	jobID, err := store.CreateJob(ctx, issueID, "myproject", 3)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimedID, err := store.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimedID != jobID {
		t.Fatalf("claimed job %q, want %q", claimedID, jobID)
	}

	stalePath := filepath.Join(cfg.ReposRoot, "worktrees", jobID)
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatalf("mkdir stale path: %v", err)
	}
	marker := filepath.Join(stalePath, "stale-marker.txt")
	if err := os.WriteFile(marker, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}

	var callCount int
	provider := stubProvider{
		run: func(ctx context.Context, workDir, prompt string) (llm.Response, error) {
			callCount++
			switch callCount {
			case 1:
				return llm.Response{Text: "Plan"}, nil
			case 2:
				return llm.Response{Text: "Implemented"}, nil
			case 3:
				return llm.Response{Text: "APPROVED"}, nil
			default:
				return llm.Response{}, nil
			}
		},
	}

	runner := New(store, provider, cfg)
	if err := runner.Run(ctx, jobID); err != nil {
		t.Fatalf("run pipeline: %v", err)
	}

	job, err := store.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.State != "ready" {
		t.Fatalf("expected ready state, got %q", job.State)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected stale marker to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(stalePath, ".git")); err != nil {
		t.Fatalf("expected cloned repo at stale path: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 provider calls, got %d", callCount)
	}
}

func createBareRemoteWithMain(t *testing.T, root string) string {
	t.Helper()

	remote := filepath.Join(root, "remote.git")
	runGitCmdLocal(t, "", "init", "--bare", remote)

	seed := filepath.Join(root, "seed")
	runGitCmdLocal(t, "", "init", seed)
	runGitCmdLocal(t, seed, "config", "user.email", "test@example.com")
	runGitCmdLocal(t, seed, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitCmdLocal(t, seed, "add", "README.md")
	runGitCmdLocal(t, seed, "commit", "-m", "initial commit")
	runGitCmdLocal(t, seed, "branch", "-M", "main")
	runGitCmdLocal(t, seed, "remote", "add", "origin", remote)
	runGitCmdLocal(t, seed, "push", "-u", "origin", "main")

	return remote
}

func runGitCmdLocal(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
