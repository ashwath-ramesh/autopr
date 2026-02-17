package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create config template and initialize database",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	// Create config template if it doesn't exist.
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := os.WriteFile(cfgPath, []byte(configTemplate), 0o644); err != nil {
			return fmt.Errorf("write config template: %w", err)
		}
		fmt.Printf("Created config template: %s\n", cfgPath)
	} else {
		fmt.Printf("Config already exists: %s\n", cfgPath)
	}

	// Load config and init DB.
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	fmt.Printf("Database initialized: %s\n", cfg.DBPath)
	fmt.Println("Edit fixflow.toml to configure your projects, then run: ff start")
	return nil
}

const configTemplate = `# FixFlow configuration
# See: https://github.com/fixflow/fixflow

db_path = "fixflow.db"
repos_root = ".repos"
log_level = "info"              # debug|info|warn|error
log_file = ""                   # empty = stderr only

[daemon]
webhook_port = 8080
webhook_secret = ""             # override via FIXFLOW_WEBHOOK_SECRET env var
max_workers = 3
max_iterations = 3              # implement<->review loop default
sync_interval = "5m"            # GitHub/Sentry poll interval
pid_file = "fixflow.pid"

[tokens]
# Override via env: GITLAB_TOKEN, GITHUB_TOKEN, SENTRY_TOKEN
gitlab = ""
github = ""
sentry = ""

[sentry]
base_url = "https://sentry.io"

[llm]
provider = "claude"             # claude|codex

[[projects]]
name = "my-project"
repo_url = "git@gitlab.com:org/repo.git"
test_cmd = "go test ./..."
base_branch = "main"

  [projects.gitlab]
  base_url = "https://gitlab.com"
  project_id = "12345"

  # [projects.github]
  # owner = "org"
  # repo = "repo"

  # [projects.sentry]
  # org = "my-org"
  # project = "my-project"

  [projects.prompts]
  plan = "templates/prompts/plan.md"
  plan_review = "templates/prompts/plan_review.md"
  implement = "templates/prompts/implement.md"
  code_review = "templates/prompts/code_review.md"
`
