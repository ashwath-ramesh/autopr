package git

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// EnsureClone clones the repo if it doesn't exist, otherwise fetches.
func EnsureClone(ctx context.Context, repoURL, localPath, token string) error {
	if _, err := os.Stat(localPath); err == nil {
		return Fetch(ctx, localPath)
	}
	authURL := injectToken(repoURL, token)
	slog.Info("cloning repository", "url", repoURL, "path", localPath)
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}
	// Init as bare repo with origin configured so origin/* refs work with worktrees.
	if err := runGit(ctx, localPath, "init", "--bare"); err != nil {
		return err
	}
	if err := runGit(ctx, localPath, "remote", "add", "origin", authURL); err != nil {
		return err
	}
	return Fetch(ctx, localPath)
}

// Fetch fetches all refs in the bare repo.
func Fetch(ctx context.Context, localPath string) error {
	return runGit(ctx, localPath, "fetch", "--all", "--prune")
}

// LatestCommit returns the HEAD commit SHA in the given directory.
func LatestCommit(ctx context.Context, dir string) (string, error) {
	out, err := runGitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommitAll stages all changes (including new files) and commits with the given message.
func CommitAll(ctx context.Context, dir, message string) (string, error) {
	// Stage everything — LLM tools create new files that need to be included.
	if err := runGit(ctx, dir, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	// Check if there's anything to commit.
	out, err := runGitOutput(ctx, dir, "diff", "--cached", "--quiet")
	if err == nil {
		// No diff means nothing staged.
		return "", fmt.Errorf("nothing to commit")
	}
	// err != nil means there are staged changes (diff --cached returns exit 1).
	_ = out

	if err := runGit(ctx, dir, "commit", "-m", message); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	return LatestCommit(ctx, dir)
}

// PushBranch pushes a branch to origin.
// NOTE: This requires Contents: Read and write on the GitHub fine-grained PAT.
// With read-only access, this call will fail with a permission error.
func PushBranch(ctx context.Context, dir, branchName string) error {
	return PushBranchToRemote(ctx, dir, "origin", branchName)
}

// PushBranchWithLease pushes a branch with --force-with-lease.
func PushBranchWithLease(ctx context.Context, dir, branchName string) error {
	return PushBranchWithLeaseToRemote(ctx, dir, "origin", branchName)
}

// PushBranchToRemote pushes a branch to the named remote.
func PushBranchToRemote(ctx context.Context, dir, remote, branchName string) error {
	return PushBranchToRemoteWithToken(ctx, dir, remote, branchName, "")
}

// PushBranchWithLeaseToRemote pushes a branch with --force-with-lease.
func PushBranchWithLeaseToRemote(ctx context.Context, dir, remote, branchName string) error {
	return PushBranchWithLeaseToRemoteWithToken(ctx, dir, remote, branchName, "")
}

// PushBranchToRemoteWithToken pushes a branch to the named remote.
//
// If token is provided and the remote is HTTPS, this uses token-authenticated
// URL for this command only so credentials are not persisted in remote config.
func PushBranchToRemoteWithToken(ctx context.Context, dir, remoteName, branchName, token string) error {
	return pushBranchToRemote(ctx, dir, remoteName, branchName, false, token, false)
}

// PushBranchWithLeaseCaptured pushes a branch with --force-with-lease without
// writing output to the process stdout/stderr (safe for TUI callers).
func PushBranchWithLeaseCaptured(ctx context.Context, dir, branchName string) error {
	return PushBranchWithLeaseCapturedToRemote(ctx, dir, "origin", branchName)
}

// PushBranchWithLeaseCapturedToRemote pushes a branch with --force-with-lease without
// writing output to the process stdout/stderr (safe for TUI callers).
func PushBranchWithLeaseCapturedToRemote(ctx context.Context, dir, remote, branchName string) error {
	return PushBranchWithLeaseCapturedToRemoteWithToken(ctx, dir, remote, branchName, "")
}

// PushBranchWithLeaseToRemoteWithToken pushes a branch with --force-with-lease.
func PushBranchWithLeaseToRemoteWithToken(ctx context.Context, dir, remoteName, branchName, token string) error {
	return pushBranchToRemote(ctx, dir, remoteName, branchName, true, token, false)
}

// PushBranchCaptured pushes a branch to origin without writing output to the
// process stdout/stderr. Any git output is captured and included in errors.
func PushBranchCaptured(ctx context.Context, dir, branchName string) error {
	return PushBranchCapturedToRemote(ctx, dir, "origin", branchName)
}

// PushBranchCapturedToRemote pushes a branch to the named remote without writing output
// to the process stdout/stderr. Any git output is captured and included in errors.
func PushBranchCapturedToRemote(ctx context.Context, dir, remote, branchName string) error {
	return PushBranchCapturedToRemoteWithToken(ctx, dir, remote, branchName, "")
}

func PushBranchWithLeaseCapturedToRemoteWithToken(ctx context.Context, dir, remoteName, branchName, token string) error {
	return pushBranchToRemote(ctx, dir, remoteName, branchName, true, token, true)
}

// PushBranchCapturedToRemoteWithToken pushes a branch to the named remote and
// captures output so stdout/stderr can be returned in errors.
func PushBranchCapturedToRemoteWithToken(ctx context.Context, dir, remoteName, branchName, token string) error {
	return pushBranchToRemote(ctx, dir, remoteName, branchName, false, token, true)
}

func pushBranchToRemote(ctx context.Context, dir, remoteName, branchName string, forceWithLease bool, token string, captured bool) error {
	remoteName = strings.TrimSpace(remoteName)
	branchName = strings.TrimSpace(branchName)
	if remoteName == "" {
		return fmt.Errorf("remote name is empty")
	}
	if branchName == "" {
		return fmt.Errorf("branch name is empty")
	}

	args := []string{"push", remoteName}
	if forceWithLease {
		args = append(args, "--force-with-lease")
	}
	args = append(args, branchName)

	if token == "" {
		if captured {
			return runGitCaptured(ctx, dir, args...)
		}
		return runGit(ctx, dir, args...)
	}

	remoteURL, err := getRemoteURL(ctx, dir, remoteName)
	if err != nil {
		return err
	}
	authURL := injectToken(remoteURL, token)
	if authURL == remoteURL {
		if captured {
			return runGitCaptured(ctx, dir, args...)
		}
		return runGit(ctx, dir, args...)
	}

	config := map[string]string{
		fmt.Sprintf("remote.%s.pushurl", remoteName): authURL,
	}

	if captured {
		return runGitCapturedWithConfig(ctx, dir, config, args...)
	}
	return runGitWithConfig(ctx, dir, config, args...)
}

// DeleteRemoteBranch deletes a branch from origin in the given repository.
// Callers decide whether a failure should be fatal.
func DeleteRemoteBranch(ctx context.Context, dir, branchName string) error {
	branchName = strings.TrimSpace(branchName)
	if branchName == "" {
		return fmt.Errorf("branch name is empty")
	}
	return runGit(ctx, dir, "push", "origin", "--delete", branchName)
}

// EnsureRemote configures a named remote URL.
//
// If the remote already exists, the existing URL must match exactly.
func EnsureRemote(ctx context.Context, dir, remoteName, remoteURL string) error {
	remoteName = strings.TrimSpace(remoteName)
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteName == "" {
		return fmt.Errorf("remote name is empty")
	}
	if remoteURL == "" {
		return fmt.Errorf("remote URL is empty")
	}

	existingRaw, errOut, err := runGitOutputAndErr(ctx, dir, "remote", "get-url", remoteName)
	if err == nil {
		if strings.TrimSpace(existingRaw) == remoteURL {
			return nil
		}
		return fmt.Errorf("remote %q exists with different URL %q", remoteName, strings.TrimSpace(existingRaw))
	}

	errText := strings.ToLower(strings.TrimSpace(errOut))
	if errText == "" {
		errText = strings.ToLower(err.Error())
	}
	if !isMissingGitRemoteError(errText) {
		return fmt.Errorf("get remote %q url: %w: %s", remoteName, err, errText)
	}
	return runGit(ctx, dir, "remote", "add", remoteName, remoteURL)
}

func isMissingGitRemoteError(errText string) bool {
	return strings.Contains(errText, "no such remote") ||
		strings.Contains(errText, "did not resolve to a git repository") ||
		strings.Contains(errText, "does not appear to be a git repository")
}

// CheckGitRemoteReachable checks that the given remote URL responds to ls-remote.
func CheckGitRemoteReachable(ctx context.Context, remoteURL, token string) error {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return fmt.Errorf("remote URL is empty")
	}
	authURL := injectToken(remoteURL, token)
	if _, _, err := runGitOutputAndErr(ctx, "", "ls-remote", authURL); err != nil {
		return fmt.Errorf("check remote reachability: %w", err)
	}
	return nil
}

func getRemoteURL(ctx context.Context, dir, remoteName string) (string, error) {
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		return "", fmt.Errorf("remote name is empty")
	}

	existingRaw, _, err := runGitOutputAndErr(ctx, dir, "remote", "get-url", remoteName)
	if err != nil {
		return "", err
	}
	existingURL := strings.TrimSpace(existingRaw)
	if existingURL == "" {
		return "", fmt.Errorf("remote %q has empty URL", remoteName)
	}
	return existingURL, nil
}

func injectToken(repoURL, token string) string {
	if token == "" {
		return repoURL
	}
	// For HTTPS URLs, inject token as oauth2 credential.
	if strings.HasPrefix(repoURL, "https://") {
		base := strings.TrimPrefix(repoURL, "https://")
		if at := strings.Index(base, "@"); at >= 0 {
			base = base[at+1:]
		}
		return "https://oauth2:" + token + "@" + base
	}
	return repoURL
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func runGitWithConfig(ctx context.Context, dir string, config map[string]string, args ...string) error {
	if len(config) == 0 {
		return runGit(ctx, dir, args...)
	}

	runArgs := make([]string, 0, len(config)*2+len(args))
	for key, value := range config {
		runArgs = append(runArgs, "-c", fmt.Sprintf("%s=%s", key, value))
	}
	runArgs = append(runArgs, args...)
	return runGit(ctx, dir, runArgs...)
}

func runGitOutputAndErr(ctx context.Context, dir string, args ...string) (string, string, error) {
	return runGitOutputAndErrWithNoEditorSetting(ctx, dir, false, args...)
}

func runGitOutputAndErrWithNoEditor(ctx context.Context, dir string, args ...string) (string, string, error) {
	return runGitOutputAndErrWithNoEditorSetting(ctx, dir, true, args...)
}

func runGitOutputAndErrWithNoEditorSetting(ctx context.Context, dir string, noEditor bool, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if noEditor {
		cmd.Env = append(cmd.Environ(), "GIT_EDITOR=true")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), nil
	}
	return stdout.String(), stderr.String(), err
}

func runGitCaptured(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func runGitCapturedWithConfig(ctx context.Context, dir string, config map[string]string, args ...string) error {
	if len(config) == 0 {
		return runGitCaptured(ctx, dir, args...)
	}

	runArgs := make([]string, 0, len(config)*2+len(args))
	for key, value := range config {
		runArgs = append(runArgs, "-c", fmt.Sprintf("%s=%s", key, value))
	}
	runArgs = append(runArgs, args...)

	cmd := exec.CommandContext(ctx, "git", runArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("git %s: %w: %s", strings.Join(runArgs, " "), err, msg)
		}
		return fmt.Errorf("git %s: %w", strings.Join(runArgs, " "), err)
	}
	return nil
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
