package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	listProject string
	listState   string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs with filters",
	RunE:  runList,
}

func init() {
	listCmd.Flags().StringVar(&listProject, "project", "", "filter by project name")
	listCmd.Flags().StringVar(&listState, "state", "all", "filter by state")
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	jobs, err := store.ListJobs(cmd.Context(), listProject, listState)
	if err != nil {
		return err
	}

	if jsonOut {
		printJSON(jobs)
		return nil
	}

	if len(jobs) == 0 {
		fmt.Println("No jobs found.")
		return nil
	}

	fmt.Printf("%-20s %-12s %-15s %-4s %-20s\n", "JOB ID", "STATE", "PROJECT", "ITER", "UPDATED")
	fmt.Println(strings.Repeat("-", 75))
	for _, j := range jobs {
		fmt.Printf("%-20s %-12s %-15s %d/%-2d %-20s\n",
			truncate(j.ID, 20), j.State, truncate(j.ProjectName, 15), j.Iteration, j.MaxIterations, j.UpdatedAt)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
