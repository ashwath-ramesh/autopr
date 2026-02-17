package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"autopr/internal/config"
	"autopr/internal/db"

	"github.com/spf13/cobra"
)

var (
	cfgPath string
	verbose bool
	jsonOut bool
	version = "dev"
	commit  = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "ap",
	Short:   "AutoPR — autonomous issue-to-PR daemon",
	Long:    "AutoPR watches your GitLab/GitHub/Sentry issues, then uses an LLM to plan, implement, test, and push fixes — ready for human approval.",
	Version: fmt.Sprintf("%s (%s)", version, commit),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		level := slog.LevelInfo
		if verbose {
			level = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "autopr.toml", "config file path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output JSON")
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return err
	}
	return nil
}

func loadConfig() (*config.Config, error) {
	return config.Load(cfgPath)
}

func openStore(cfg *config.Config) (*db.Store, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	// Clean up orphaned WAL sidecar files if the main DB was deleted.
	if _, err := os.Stat(cfg.DBPath); os.IsNotExist(err) {
		_ = os.Remove(cfg.DBPath + "-shm")
		_ = os.Remove(cfg.DBPath + "-wal")
	}
	return db.Open(cfg.DBPath)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// resolveJob resolves a full or partial job ID from CLI args.
func resolveJob(store *db.Store, arg string) (string, error) {
	return store.ResolveJobID(context.Background(), arg)
}
