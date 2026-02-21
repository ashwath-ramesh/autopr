package git

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushBranchCapturedSuccessDoesNotWriteToTerminal(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)

	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", repo)
	runGitCmd(t, repo, "config", "user.email", "test@example.com")
	runGitCmd(t, repo, "config", "user.name", "Test User")
	runGitCmd(t, repo, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitCmd(t, repo, "add", "README.md")
	runGitCmd(t, repo, "commit", "-m", "init")
	runGitCmd(t, repo, "checkout", "-b", "feature/test")

	origStdout := os.Stdout
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	os.Stderr = w
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	if err := PushBranchCaptured(ctx, repo, "feature/test"); err != nil {
		t.Fatalf("push branch captured: %v", err)
	}

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no terminal output, got: %q", string(out))
	}
}

func TestPushBranchCapturedIncludesStderrInError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)

	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", repo)
	runGitCmd(t, repo, "remote", "add", "origin", remote)

	err := PushBranchCaptured(ctx, repo, "missing-branch")
	if err == nil {
		t.Fatal("expected push failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "src refspec") || !strings.Contains(msg, "missing-branch") {
		t.Fatalf("expected stderr details in error, got: %v", err)
	}
}

func TestPushBranchToRemoteWritesToNamedRemote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)

	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", repo)
	runGitCmd(t, repo, "config", "user.email", "test@example.com")
	runGitCmd(t, repo, "config", "user.name", "Test User")
	runGitCmd(t, repo, "remote", "add", "upstream", remote)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitCmd(t, repo, "add", "README.md")
	runGitCmd(t, repo, "commit", "-m", "init")
	runGitCmd(t, repo, "branch", "-M", "main")

	destination := filepath.Join(tmp, "fork.git")
	runGitCmd(t, "", "init", "--bare", destination)

	if err := PushBranchToRemote(ctx, repo, "origin", "main"); err == nil {
		t.Fatal("expected push to missing origin to fail")
	}

	if err := PushBranchToRemote(ctx, repo, "upstream", "main"); err != nil {
		t.Fatalf("push to upstream: %v", err)
	}
	if err := PushBranchToRemote(ctx, repo, "origin", "main"); err == nil {
		t.Fatal("expected push to missing origin to fail again")
	}

	if err := PushBranchToRemote(ctx, repo, "origin", "main"); err == nil {
		// Sanity assert to ensure we actually touched destination remote config check.
		t.Fatal("expected destination remote not added yet")
	}

	// Add destination as named remote and push there.
	if err := PushBranchToRemote(ctx, repo, "destination", "main"); err == nil {
		t.Fatal("expected push to destination to fail before remote exists")
	}
	runGitCmd(t, repo, "remote", "add", "destination", destination)
	if err := PushBranchToRemote(ctx, repo, "destination", "main"); err != nil {
		t.Fatalf("push to destination: %v", err)
	}

	out := runGitCmdOutput(t, "", "ls-remote", "--heads", destination, "main")
	if !strings.Contains(out, "refs/heads/main") {
		t.Fatalf("expected branch pushed to destination, got %q", out)
	}
}

func TestEnsureRemoteAddsAndMatchesExistingURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	remoteURL := filepath.Join(tmp, "origin.git")
	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", "--bare", remoteURL)
	runGitCmd(t, "", "init", repo)

	if err := EnsureRemote(ctx, repo, "origin", remoteURL); err != nil {
		t.Fatalf("add origin remote: %v", err)
	}
	if err := EnsureRemote(ctx, repo, "origin", remoteURL); err != nil {
		t.Fatalf("ensure matching origin remote: %v", err)
	}
}

func TestEnsureRemoteRejectsDifferentURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()
	remoteA := filepath.Join(tmp, "a.git")
	remoteB := filepath.Join(tmp, "b.git")
	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", "--bare", remoteA)
	runGitCmd(t, "", "init", "--bare", remoteB)
	runGitCmd(t, "", "init", repo)
	if err := runGitCmdOutputErr(t, repo, "remote", "add", "origin", remoteA); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	if err := EnsureRemote(ctx, repo, "origin", remoteB); err == nil {
		t.Fatalf("expected different URL error")
	}
}

func TestCheckGitRemoteReachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

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

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)
	runGitCmd(t, seed, "remote", "add", "origin", remote)
	runGitCmd(t, seed, "push", "origin", "main")

	if err := CheckGitRemoteReachable(ctx, remote, ""); err != nil {
		t.Fatalf("expected reachable local remote: %v", err)
	}
	if err := CheckGitRemoteReachable(ctx, filepath.Join(tmp, "missing.git"), ""); err == nil {
		t.Fatal("expected unreachable remote error")
	}
}

func TestDeleteRemoteBranchSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)

	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", repo)
	runGitCmd(t, repo, "config", "user.email", "test@example.com")
	runGitCmd(t, repo, "config", "user.name", "Test User")
	runGitCmd(t, repo, "remote", "add", "origin", remote)
	runGitCmd(t, repo, "checkout", "-B", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitCmd(t, repo, "add", "README.md")
	runGitCmd(t, repo, "commit", "-m", "init")
	runGitCmd(t, repo, "push", "origin", "main")
	runGitCmd(t, repo, "checkout", "-b", "autopr/test-delete")
	runGitCmd(t, repo, "push", "origin", "autopr/test-delete")

	if err := DeleteRemoteBranch(ctx, repo, "autopr/test-delete"); err != nil {
		t.Fatalf("delete remote branch: %v", err)
	}

	cmd := exec.Command("git", "ls-remote", "--heads", remote, "autopr/test-delete")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ls-remote remote: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected branch to be deleted on remote, got: %s", strings.TrimSpace(string(out)))
	}
}

func TestDeleteRemoteBranchFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()

	remote := filepath.Join(tmp, "remote.git")
	runGitCmd(t, "", "init", "--bare", remote)

	repo := filepath.Join(tmp, "repo")
	runGitCmd(t, "", "init", repo)
	runGitCmd(t, repo, "config", "user.email", "test@example.com")
	runGitCmd(t, repo, "config", "user.name", "Test User")
	runGitCmd(t, repo, "remote", "add", "origin", remote)
	runGitCmd(t, repo, "checkout", "-B", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitCmd(t, repo, "add", "README.md")
	runGitCmd(t, repo, "commit", "-m", "init")
	runGitCmd(t, repo, "push", "origin", "main")

	if err := DeleteRemoteBranch(ctx, repo, "autopr/does-not-exist"); err == nil {
		t.Fatalf("expected delete remote branch failure")
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
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

func runGitCmdOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func runGitCmdOutputErr(t *testing.T, dir string, args ...string) error {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	_, err := cmd.Output()
	return err
}
