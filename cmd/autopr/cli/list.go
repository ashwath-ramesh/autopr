package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"autopr/internal/cost"
	"autopr/internal/db"

	"github.com/spf13/cobra"
)

var (
	listProject  string
	listState    string
	listSort     string
	listAsc      bool
	listDesc     bool
	listCost     bool
	listPage     int
	listPageSize int
	listAll      bool
	listWatch    bool
	listInterval time.Duration
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs with filters",
	RunE:  runList,
}

func init() {
	listCmd.Flags().StringVar(&listProject, "project", "", "filter by project name")
	listCmd.Flags().StringVar(&listState, "state", "all", "filter by state")
	listCmd.Flags().StringVar(&listSort, "sort", "updated_at", "sort by field: updated_at, created_at, state, or project")
	listCmd.Flags().BoolVar(&listAsc, "asc", false, "sort in ascending order")
	listCmd.Flags().BoolVar(&listDesc, "desc", false, "sort in descending order (default)")
	listCmd.Flags().BoolVar(&listCost, "cost", false, "show estimated cost column")
	listCmd.Flags().IntVar(&listPage, "page", 1, "page number (1-based)")
	listCmd.Flags().IntVar(&listPageSize, "page-size", 20, "number of rows per page")
	listCmd.Flags().BoolVar(&listAll, "all", false, "disable pagination and show full output")
	listCmd.Flags().BoolVar(&listWatch, "watch", false, "refresh output periodically")
	listCmd.Flags().DurationVar(&listInterval, "interval", defaultWatchInterval, "refresh interval (e.g. 5s, 2s, 500ms)")
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	sortBy, err := normalizeListSort(listSort)
	if err != nil {
		return err
	}
	state, err := normalizeListState(listState)
	if err != nil {
		return err
	}
	if listAsc && listDesc {
		return fmt.Errorf("--asc and --desc cannot be used together")
	}
	ascending := listAsc

	store, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	paginate := !listAll && (cmd.Flags().Changed("page") || cmd.Flags().Changed("page-size"))
	page := listPage
	pageSize := listPageSize
	snapshot := func(ctx context.Context) (listSnapshot, error) {
		return collectListSnapshot(ctx, store, listProject, state, sortBy, ascending, paginate, page, pageSize, listCost)
	}

	render := func(ctx context.Context, snapshot listSnapshot, iteration int64) error {
		return renderListSnapshot(ctx, jsonOut, listWatch, iteration, snapshot, listCost)
	}

	if listWatch {
		if listInterval <= 0 {
			return fmt.Errorf("invalid interval %v; expected > 0", listInterval)
		}
		return runWatchLoop(cmd.Context(), listInterval, func(ctx context.Context, iteration int64) error {
			s, err := snapshot(ctx)
			if err != nil {
				return err
			}
			return render(ctx, s, iteration)
		})
	}

	s, err := snapshot(cmd.Context())
	if err != nil {
		return err
	}
	return render(cmd.Context(), s, 0)
}

type listSnapshot struct {
	Jobs     []db.Job
	Total    int
	Page     int
	PageSize int
	Paginate bool
	Cost     map[string]db.TokenSummary
}

func collectListSnapshot(ctx context.Context, store *db.Store, project, state, sortBy string, ascending bool, paginate bool, page int, pageSize int, withCost bool) (listSnapshot, error) {
	if paginate {
		if page < 1 {
			return listSnapshot{}, fmt.Errorf("invalid page value %d; expected >= 1", page)
		}
		if pageSize < 1 {
			return listSnapshot{}, fmt.Errorf("invalid page-size value %d; expected >= 1", pageSize)
		}
	}

	var jobs []db.Job
	total := 0
	if paginate {
		var err error
		jobs, total, err = store.ListJobsPage(ctx, project, state, sortBy, ascending, page, pageSize)
		if err != nil {
			return listSnapshot{}, err
		}
	} else {
		var err error
		jobs, err = store.ListJobs(ctx, project, state, sortBy, ascending)
		if err != nil {
			return listSnapshot{}, err
		}
	}

	// Optionally fetch cost data.
	var costMap map[string]db.TokenSummary
	if withCost && len(jobs) > 0 {
		ids := make([]string, len(jobs))
		for i, j := range jobs {
			ids[i] = j.ID
		}
		costMap, _ = store.AggregateTokensForJobs(ctx, ids)
	}

	return listSnapshot{
		Jobs:     jobs,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Paginate: paginate,
		Cost:     costMap,
	}, nil
}

func renderListSnapshot(_ context.Context, asJSON bool, compactJSON bool, iteration int64, snapshot listSnapshot, showCost bool) error {
	if asJSON {
		if compactJSON {
			payload := struct {
				Jobs      []db.Job `json:"jobs"`
				Page      int      `json:"page"`
				PageSize  int      `json:"page_size"`
				Total     int      `json:"total"`
				Iteration int64    `json:"iteration"`
			}{
				Jobs:      snapshot.Jobs,
				Iteration: iteration,
			}
			if snapshot.Paginate {
				payload.Page = snapshot.Page
				payload.PageSize = snapshot.PageSize
				payload.Total = snapshot.Total
			} else {
				payload.Total = len(snapshot.Jobs)
			}
			return writeJSONLine(payload)
		}
		if snapshot.Paginate {
			payload := struct {
				Jobs     []db.Job `json:"jobs"`
				Page     int      `json:"page"`
				PageSize int      `json:"page_size"`
				Total    int      `json:"total"`
			}{
				Jobs:     snapshot.Jobs,
				Page:     snapshot.Page,
				PageSize: snapshot.PageSize,
				Total:    snapshot.Total,
			}
			printJSON(payload)
			return nil
		}
		printJSON(snapshot.Jobs)
		return nil
	}

	if snapshot.Paginate {
		pages := 0
		if snapshot.Total > 0 {
			pages = (snapshot.Total + snapshot.PageSize - 1) / snapshot.PageSize
		}
		if err := writef("Page %d/%d, total rows: %d\n", snapshot.Page, pages, snapshot.Total); err != nil {
			return err
		}
	}

	if len(snapshot.Jobs) == 0 && !snapshot.Paginate {
		return writef("No jobs found. Run 'ap start' to begin processing issues.\n")
	}

	if showCost {
		if err := writef("%-10s %-20s %-13s %-13s %-5s %-8s %-45s %s\n", "JOB", "STATE", "PROJECT", "SOURCE", "RETRY", "COST", "ISSUE", "UPDATED"); err != nil {
			return err
		}
	} else if err := writef("%-10s %-20s %-13s %-13s %-5s %-55s %s\n", "JOB", "STATE", "PROJECT", "SOURCE", "RETRY", "ISSUE", "UPDATED"); err != nil {
		return err
	}
	if err := writef("%s\n", strings.Repeat("-", 136)); err != nil {
		return err
	}
	total := len(snapshot.Jobs)
	queued, active, failed, merged := 0, 0, 0, 0
	for _, j := range snapshot.Jobs {
		source := ""
		if j.IssueSource != "" && j.SourceIssueID != "" {
			source = fmt.Sprintf("%s #%s", capitalize(j.IssueSource), j.SourceIssueID)
		}

		if showCost {
			costStr := "-"
			if ts, ok := snapshot.Cost[j.ID]; ok && ts.SessionCount > 0 {
				c := cost.Calculate(ts.Provider, ts.TotalInputTokens, ts.TotalOutputTokens)
				costStr = cost.FormatUSD(c)
			}
			title := truncate(j.IssueTitle, 45)
			if err := writef("%-10s %-20s %-13s %-13s %-5s %-8s %-45s %s\n",
				db.ShortID(j.ID), db.DisplayState(j.State, j.PRMergedAt, j.PRClosedAt), truncate(j.ProjectName, 12), source,
				fmt.Sprintf("%d/%d", j.Iteration, j.MaxIterations),
				costStr, title, j.UpdatedAt); err != nil {
				return err
			}
		} else {
			title := truncate(j.IssueTitle, 55)
			if err := writef("%-10s %-20s %-13s %-13s %-5s %-55s %s\n",
				db.ShortID(j.ID), db.DisplayState(j.State, j.PRMergedAt, j.PRClosedAt), truncate(j.ProjectName, 12), source,
				fmt.Sprintf("%d/%d", j.Iteration, j.MaxIterations),
				title, j.UpdatedAt); err != nil {
				return err
			}
		}

		if j.State == "queued" {
			queued++
		}
		if isActiveState(j.State) {
			active++
		}
		switch j.State {
		case "failed", "rejected", "cancelled":
			failed++
		}
		if j.State == "approved" && j.PRMergedAt != "" {
			merged++
		}
	}
	return writef("Total: %d jobs (%d queued, %d active, %d failed, %d merged)\n", total, queued, active, failed, merged)
}

func normalizeListSort(sortBy string) (string, error) {
	switch sortBy {
	case "updated_at", "created_at", "state", "project":
		return sortBy, nil
	default:
		return "", fmt.Errorf("invalid --sort %q (expected one of: updated_at, created_at, state, project)", sortBy)
	}
}

func normalizeListState(state string) (string, error) {
	if state == "resolving" {
		return "resolving_conflicts", nil
	}

	switch state {
	case "all", "active", "merged", "queued", "planning", "implementing", "reviewing", "testing", "ready", "rebasing", "resolving_conflicts", "awaiting_checks", "approved", "rejected", "failed", "cancelled":
		return state, nil
	default:
		return "", fmt.Errorf("invalid --state %q (expected one of: all, active, merged, queued, planning, implementing, reviewing, testing, ready, rebasing, resolving, resolving_conflicts, awaiting_checks, approved, rejected, failed, cancelled)", state)
	}
}

func isActiveState(state string) bool {
	switch state {
	case "planning", "implementing", "reviewing", "testing", "rebasing", "resolving_conflicts", "awaiting_checks":
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
