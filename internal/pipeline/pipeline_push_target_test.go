package pipeline

import (
	"context"
	"testing"

	"autopr/internal/config"
)

func TestResolveGitHubPushTarget_DefaultOrigin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	upstreamRemote := createBareRemoteWithMain(t, tmp)

	cfg := &config.Config{
		Projects: []config.ProjectConfig{{
			Name:       "myproject",
			RepoURL:    upstreamRemote,
			GitHub:     &config.ProjectGitHub{Owner: "acme", Repo: "repo"},
			BaseBranch: "main",
			TestCmd:    "true",
		}},
		Tokens: config.TokensConfig{GitHub: "token"},
	}
	worktree := t.TempDir()
	runGitCmdLocal(t, "", "clone", upstreamRemote, worktree)

	remote, head, err := ResolveGitHubPushTarget(ctx, &cfg.Projects[0], "feature/default", worktree, cfg.Tokens.GitHub)
	if err != nil {
		t.Fatalf("resolve push target: %v", err)
	}
	if remote != "origin" {
		t.Fatalf("expected origin, got %q", remote)
	}
	if head != "feature/default" {
		t.Fatalf("expected unqualified head, got %q", head)
	}
}

func TestResolveGitHubPushTarget_ForkOwnerValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	upstreamRemote := createBareRemoteWithMain(t, tmp)

	cfg := &config.Config{
		Projects: []config.ProjectConfig{{
			Name:       "myproject",
			RepoURL:    upstreamRemote,
			GitHub:     &config.ProjectGitHub{Owner: "acme", Repo: "repo", ForkOwner: "my-fork"},
			BaseBranch: "main",
			TestCmd:    "true",
		}},
		Tokens: config.TokensConfig{GitHub: "token"},
	}
	worktree := t.TempDir()
	runGitCmdLocal(t, "", "clone", upstreamRemote, worktree)

	_, _, err := ResolveGitHubPushTarget(ctx, &cfg.Projects[0], "feature/fork", worktree, "")
	if err == nil {
		t.Fatalf("expected token validation error with fork owner set")
	}
}
