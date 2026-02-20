package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"autopr/internal/config"
	"autopr/internal/update"

	"github.com/spf13/cobra"
)

var upgradeCheckOnly bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade ap to the latest release",
	RunE:  runUpgrade,
}

type upgradeService interface {
	Check(context.Context, string) (update.CheckResult, error)
	Upgrade(context.Context, string) (update.UpgradeResult, error)
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheckOnly, "check", false, "Check for updates without installing")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	err := runUpgradeWith(cmd.Context(), os.Stdout, update.NewManager(version), version, upgradeCheckOnly)
	if err != nil {
		return err
	}

	// Best-effort config migration after a successful upgrade.
	if !upgradeCheckOnly {
		if path, pathErr := resolveConfigPath(); pathErr == nil {
			if migErr := config.MigrateConfigFile(path); migErr != nil {
				slog.Warn("post-upgrade config migration skipped", "err", migErr)
			}
		}
	}
	return nil
}

func runUpgradeWith(ctx context.Context, out io.Writer, svc upgradeService, currentVersion string, checkOnly bool) error {
	if checkOnly {
		res, err := svc.Check(ctx, currentVersion)
		if err != nil {
			return err
		}
		if res.UpdateAvailable {
			fmt.Fprintf(out, "update available: %s (current: %s)\n", res.LatestVersion, res.CurrentVersion)
			return nil
		}
		fmt.Fprintf(out, "already up to date (%s)\n", nonEmptyVersion(res.CurrentVersion, currentVersion))
		return nil
	}

	res, err := svc.Upgrade(ctx, currentVersion)
	if err != nil {
		return err
	}
	if !res.UpdateAvailable {
		fmt.Fprintf(out, "already up to date (%s)\n", nonEmptyVersion(res.CurrentVersion, currentVersion))
		return nil
	}
	if res.Upgraded {
		fmt.Fprintf(out, "upgraded ap to %s\n", res.LatestVersion)
		return nil
	}
	fmt.Fprintf(out, "already up to date (%s)\n", nonEmptyVersion(res.CurrentVersion, currentVersion))
	return nil
}

func nonEmptyVersion(preferred, fallback string) string {
	if preferred != "" {
		return preferred
	}
	if fallback != "" {
		return fallback
	}
	return "unknown"
}
