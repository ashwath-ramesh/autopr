package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	DBPath   string `toml:"db_path"`
	ReposRoot string `toml:"repos_root"`
	LogLevel string `toml:"log_level"`
	LogFile  string `toml:"log_file"`

	Daemon DaemonConfig `toml:"daemon"`
	Tokens TokensConfig `toml:"tokens"`
	Sentry SentryConfig `toml:"sentry"`
	LLM    LLMConfig    `toml:"llm"`

	Projects []ProjectConfig `toml:"projects"`

	// Resolved at runtime (not in TOML).
	BaseDir string `toml:"-"`
}

type DaemonConfig struct {
	WebhookPort   int    `toml:"webhook_port"`
	WebhookSecret string `toml:"webhook_secret"`
	MaxWorkers    int    `toml:"max_workers"`
	MaxIterations int    `toml:"max_iterations"`
	SyncInterval  string `toml:"sync_interval"`
	PIDFile       string `toml:"pid_file"`
}

type TokensConfig struct {
	GitLab string `toml:"gitlab"`
	GitHub string `toml:"github"`
	Sentry string `toml:"sentry"`
}

type SentryConfig struct {
	BaseURL string `toml:"base_url"`
}

type LLMConfig struct {
	Provider string `toml:"provider"`
}

type ProjectConfig struct {
	Name       string              `toml:"name"`
	RepoURL    string              `toml:"repo_url"`
	TestCmd    string              `toml:"test_cmd"`
	BaseBranch string              `toml:"base_branch"`
	GitLab     *ProjectGitLab      `toml:"gitlab"`
	GitHub     *ProjectGitHub      `toml:"github"`
	Sentry     *ProjectSentry      `toml:"sentry"`
	Prompts    *ProjectPrompts     `toml:"prompts"`
}

type ProjectGitLab struct {
	BaseURL   string `toml:"base_url"`
	ProjectID string `toml:"project_id"`
}

type ProjectGitHub struct {
	Owner string `toml:"owner"`
	Repo  string `toml:"repo"`
}

type ProjectSentry struct {
	Org     string `toml:"org"`
	Project string `toml:"project"`
}

type ProjectPrompts struct {
	Plan       string `toml:"plan"`
	PlanReview string `toml:"plan_review"`
	Implement  string `toml:"implement"`
	CodeReview string `toml:"code_review"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	cfg.BaseDir = filepath.Dir(path)
	applyDefaults(cfg)
	applyEnvOverrides(cfg)
	warnTokensInFile(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	resolvePaths(cfg)
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.DBPath == "" {
		cfg.DBPath = "autopr.db"
	}
	if cfg.ReposRoot == "" {
		cfg.ReposRoot = ".repos"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.Daemon.WebhookPort == 0 {
		cfg.Daemon.WebhookPort = 8080
	}
	if cfg.Daemon.MaxWorkers == 0 {
		cfg.Daemon.MaxWorkers = 3
	}
	if cfg.Daemon.MaxIterations == 0 {
		cfg.Daemon.MaxIterations = 3
	}
	if cfg.Daemon.SyncInterval == "" {
		cfg.Daemon.SyncInterval = "5m"
	}
	if cfg.Daemon.PIDFile == "" {
		cfg.Daemon.PIDFile = "autopr.pid"
	}
	if cfg.Sentry.BaseURL == "" {
		cfg.Sentry.BaseURL = "https://sentry.io"
	}
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "claude"
	}
	for i := range cfg.Projects {
		if cfg.Projects[i].BaseBranch == "" {
			cfg.Projects[i].BaseBranch = "main"
		}
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("AUTOPR_WEBHOOK_SECRET"); v != "" {
		cfg.Daemon.WebhookSecret = v
	}
	if v := os.Getenv("GITLAB_TOKEN"); v != "" {
		cfg.Tokens.GitLab = v
	}
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		cfg.Tokens.GitHub = v
	}
	if v := os.Getenv("SENTRY_TOKEN"); v != "" {
		cfg.Tokens.Sentry = v
	}
}

func warnTokensInFile(cfg *Config) {
	if cfg.Tokens.GitLab != "" && os.Getenv("GITLAB_TOKEN") == "" {
		slog.Warn("gitlab token found in config file; prefer GITLAB_TOKEN env var")
	}
	if cfg.Tokens.GitHub != "" && os.Getenv("GITHUB_TOKEN") == "" {
		slog.Warn("github token found in config file; prefer GITHUB_TOKEN env var")
	}
	if cfg.Tokens.Sentry != "" && os.Getenv("SENTRY_TOKEN") == "" {
		slog.Warn("sentry token found in config file; prefer SENTRY_TOKEN env var")
	}
}

func validate(cfg *Config) error {
	switch cfg.LLM.Provider {
	case "claude", "codex":
	default:
		return fmt.Errorf("unsupported llm.provider: %q (must be claude or codex)", cfg.LLM.Provider)
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported log_level: %q", cfg.LogLevel)
	}
	if _, err := time.ParseDuration(cfg.Daemon.SyncInterval); err != nil {
		return fmt.Errorf("invalid daemon.sync_interval %q: %w", cfg.Daemon.SyncInterval, err)
	}
	if len(cfg.Projects) == 0 {
		return fmt.Errorf("at least one [[projects]] entry is required")
	}
	for i, p := range cfg.Projects {
		if p.Name == "" {
			return fmt.Errorf("projects[%d]: name is required", i)
		}
		if p.RepoURL == "" {
			return fmt.Errorf("project %q: repo_url is required", p.Name)
		}
		if p.TestCmd == "" {
			return fmt.Errorf("project %q: test_cmd is required", p.Name)
		}
		if p.GitLab == nil && p.GitHub == nil && p.Sentry == nil {
			return fmt.Errorf("project %q: at least one source (gitlab/github/sentry) is required", p.Name)
		}
	}
	return nil
}

func resolvePaths(cfg *Config) {
	cfg.DBPath = absPath(cfg.BaseDir, cfg.DBPath)
	cfg.ReposRoot = absPath(cfg.BaseDir, cfg.ReposRoot)
	cfg.Daemon.PIDFile = absPath(cfg.BaseDir, cfg.Daemon.PIDFile)
	if cfg.LogFile != "" {
		cfg.LogFile = absPath(cfg.BaseDir, cfg.LogFile)
	}
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.Prompts != nil {
			if p.Prompts.Plan != "" {
				p.Prompts.Plan = absPath(cfg.BaseDir, p.Prompts.Plan)
			}
			if p.Prompts.PlanReview != "" {
				p.Prompts.PlanReview = absPath(cfg.BaseDir, p.Prompts.PlanReview)
			}
			if p.Prompts.Implement != "" {
				p.Prompts.Implement = absPath(cfg.BaseDir, p.Prompts.Implement)
			}
			if p.Prompts.CodeReview != "" {
				p.Prompts.CodeReview = absPath(cfg.BaseDir, p.Prompts.CodeReview)
			}
		}
	}
}

func absPath(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func (cfg *Config) ProjectByName(name string) (*ProjectConfig, bool) {
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == name {
			return &cfg.Projects[i], true
		}
	}
	return nil, false
}

func (cfg *Config) SlogLevel() slog.Level {
	switch cfg.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LocalRepoPath returns the local clone path for a project.
func (cfg *Config) LocalRepoPath(projectName string) string {
	return filepath.Join(cfg.ReposRoot, sanitize(projectName))
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}
