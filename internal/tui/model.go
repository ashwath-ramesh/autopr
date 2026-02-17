package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"fixflow/internal/config"
	"fixflow/internal/db"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("37"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	stateStyle    = map[string]lipgloss.Style{
		"queued":       lipgloss.NewStyle().Foreground(lipgloss.Color("246")),
		"planning":     lipgloss.NewStyle().Foreground(lipgloss.Color("33")),
		"implementing": lipgloss.NewStyle().Foreground(lipgloss.Color("33")),
		"reviewing":    lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"testing":      lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"ready":        lipgloss.NewStyle().Foreground(lipgloss.Color("46")),
		"approved":     lipgloss.NewStyle().Foreground(lipgloss.Color("40")),
		"rejected":     lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		"failed":       lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	}
	sessStatusStyle = map[string]lipgloss.Style{
		"running":   lipgloss.NewStyle().Foreground(lipgloss.Color("33")),
		"completed": lipgloss.NewStyle().Foreground(lipgloss.Color("46")),
		"failed":    lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	}
	diffAddStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	diffDelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	diffHunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("37"))
	diffMetaStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	activeTab     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46")).Underline(true)
	inactiveTab   = dimStyle
)

// ── Model ───────────────────────────────────────────────────────────────────

// Model is the BubbleTea model for the FixFlow TUI.
//
// Navigation depth:
//
//	selected == nil                          → Level 1 (job list)
//	selected != nil && !showDiff && selectedSession == nil → Level 2 (job detail + sessions)
//	showDiff                                 → Level 2d (diff view)
//	selectedSession != nil                   → Level 3 (session detail)
type Model struct {
	store *db.Store
	cfg   *config.Config

	// Level 1: job list
	jobs   []db.Job
	cursor int

	// Level 2: job detail + session list
	selected   *db.Job
	sessions   []db.LLMSessionSummary
	sessCursor int

	// Level 2d: diff view
	showDiff   bool
	diffLines  []string
	diffOffset int

	// Level 3: session detail with scrollable output
	selectedSession *db.LLMSession
	showInput       bool     // tab toggles input/output
	scrollOffset    int
	lines           []string // pre-split content lines

	err    error
	width  int
	height int
}

func NewModel(store *db.Store, cfg *config.Config) Model {
	return Model{store: store, cfg: cfg}
}

// ── Messages ────────────────────────────────────────────────────────────────

type jobsMsg []db.Job
type sessionsMsg []db.LLMSessionSummary
type sessionMsg db.LLMSession
type diffMsg []string
type errMsg error

// ── Init / Commands ─────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd { return m.fetchJobs }

func (m Model) fetchJobs() tea.Msg {
	jobs, err := m.store.ListJobs(context.Background(), "", "all")
	if err != nil {
		return errMsg(err)
	}
	return jobsMsg(jobs)
}

func (m Model) fetchSessions() tea.Msg {
	sessions, err := m.store.ListSessionSummariesByJob(context.Background(), m.selected.ID)
	if err != nil {
		return errMsg(err)
	}
	return sessionsMsg(sessions)
}

func (m Model) fetchFullSession() tea.Msg {
	sess, err := m.store.GetFullSession(context.Background(), m.sessions[m.sessCursor].ID)
	if err != nil {
		return errMsg(err)
	}
	return sessionMsg(sess)
}

func (m Model) fetchDiff() tea.Msg {
	job := m.selected
	if job == nil || job.WorktreePath == "" {
		return diffMsg([]string{"(no worktree available)"})
	}

	baseBranch := "master"
	if p, ok := m.cfg.ProjectByName(job.ProjectName); ok && p.BaseBranch != "" {
		baseBranch = p.BaseBranch
	}

	cmd := exec.CommandContext(context.Background(),
		"git", "diff", fmt.Sprintf("origin/%s..HEAD", baseBranch))
	cmd.Dir = job.WorktreePath
	out, err := cmd.Output()
	if err != nil {
		return diffMsg([]string{fmt.Sprintf("(git diff error: %v)", err)})
	}
	if len(out) == 0 {
		return diffMsg([]string{"(no changes)"})
	}
	return diffMsg(strings.Split(string(out), "\n"))
}

// ── Update ──────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case jobsMsg:
		m.jobs = msg
		m.err = nil
	case sessionsMsg:
		m.sessions = msg
		m.sessCursor = 0
		m.err = nil
	case sessionMsg:
		sess := db.LLMSession(msg)
		m.selectedSession = &sess
		m.showInput = false
		m.scrollOffset = 0
		m.lines = splitContent(sess.ResponseText, "running", sess.Status)
	case diffMsg:
		m.diffLines = msg
		m.showDiff = true
		m.diffOffset = 0
	case errMsg:
		m.err = msg
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func splitContent(text, emptyLabel, status string) []string {
	if text != "" {
		return strings.Split(text, "\n")
	}
	if status == "running" {
		return []string{"(in progress)"}
	}
	return []string{"(no output)"}
}

// ── Key Handling ────────────────────────────────────────────────────────────

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	}

	if m.showDiff {
		return m.handleKeyDiff(key)
	}
	if m.selectedSession != nil {
		return m.handleKeyLevel3(key)
	}
	if m.selected != nil {
		return m.handleKeyLevel2(key)
	}
	return m.handleKeyLevel1(key)
}

func (m Model) handleKeyLevel1(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.jobs)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < len(m.jobs) {
			m.selected = &m.jobs[m.cursor]
			return m, m.fetchSessions
		}
	case "r":
		return m, m.fetchJobs
	}
	return m, nil
}

func (m Model) handleKeyLevel2(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.sessCursor > 0 {
			m.sessCursor--
		}
	case "down", "j":
		if m.sessCursor < len(m.sessions)-1 {
			m.sessCursor++
		}
	case "enter":
		if len(m.sessions) > 0 && m.sessCursor < len(m.sessions) {
			return m, m.fetchFullSession
		}
	case "d":
		if m.selected != nil && m.selected.WorktreePath != "" {
			return m, m.fetchDiff
		}
	case "esc":
		m.selected = nil
		m.sessions = nil
		m.sessCursor = 0
	case "r":
		return m, m.fetchSessions
	}
	return m, nil
}

func (m Model) handleKeyLevel3(key string) (tea.Model, tea.Cmd) {
	avail := m.scrollHeight()
	switch key {
	case "up", "k":
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
	case "down", "j":
		if m.scrollOffset < maxOffset(m.lines, avail) {
			m.scrollOffset++
		}
	case "u":
		m.scrollOffset -= avail / 2
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}
	case "d":
		m.scrollOffset += avail / 2
		if m.scrollOffset > maxOffset(m.lines, avail) {
			m.scrollOffset = maxOffset(m.lines, avail)
		}
	case "tab":
		m.showInput = !m.showInput
		m.scrollOffset = 0
		if m.showInput {
			if m.selectedSession.PromptText != "" {
				m.lines = strings.Split(m.selectedSession.PromptText, "\n")
			} else {
				m.lines = []string{"(no input recorded)"}
			}
		} else {
			m.lines = splitContent(m.selectedSession.ResponseText, "running", m.selectedSession.Status)
		}
	case "esc":
		m.selectedSession = nil
		m.lines = nil
		m.scrollOffset = 0
		m.showInput = false
	}
	return m, nil
}

func (m Model) handleKeyDiff(key string) (tea.Model, tea.Cmd) {
	avail := m.scrollHeight()
	switch key {
	case "up", "k":
		if m.diffOffset > 0 {
			m.diffOffset--
		}
	case "down", "j":
		if m.diffOffset < maxOffset(m.diffLines, avail) {
			m.diffOffset++
		}
	case "u":
		m.diffOffset -= avail / 2
		if m.diffOffset < 0 {
			m.diffOffset = 0
		}
	case "d":
		m.diffOffset += avail / 2
		if m.diffOffset > maxOffset(m.diffLines, avail) {
			m.diffOffset = maxOffset(m.diffLines, avail)
		}
	case "esc":
		m.showDiff = false
		m.diffLines = nil
		m.diffOffset = 0
	}
	return m, nil
}

func maxOffset(lines []string, avail int) int {
	n := len(lines) - avail
	if n < 0 {
		return 0
	}
	return n
}

// ── Views ───────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}
	if m.showDiff {
		return m.diffView()
	}
	if m.selectedSession != nil {
		return m.sessionView()
	}
	if m.selected != nil {
		return m.detailView()
	}
	return m.listView()
}

// ── Level 1: Job List ───────────────────────────────────────────────────────

func (m Model) listView() string {
	var b strings.Builder
	w := m.w()

	b.WriteString(titleStyle.Render("FIXFLOW"))
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d jobs", len(m.jobs))))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	if len(m.jobs) == 0 {
		b.WriteString(dimStyle.Render("  No jobs found. Waiting for issues..."))
		b.WriteString("\n")
	} else {
		// Header row.
		b.WriteString(headerStyle.Render(fmt.Sprintf("  %-3s %-18s %-14s %-12s %-5s %s",
			"#", "JOB ID", "STATE", "PROJECT", "ITER", "UPDATED")))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
		b.WriteString("\n")

		for i, job := range m.jobs {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}

			st, ok := stateStyle[job.State]
			if !ok {
				st = dimStyle
			}

			// Truncate job ID for display (keep prefix).
			shortID := job.ID
			if len(shortID) > 16 {
				shortID = shortID[:16] + ".."
			}

			// Format updated time — show just time portion if available.
			updated := job.UpdatedAt
			if len(updated) > 11 {
				updated = updated[11:]
			}

			line := fmt.Sprintf("%s%-3d %-18s %s %-12s %-5s %s",
				cursor,
				i+1,
				shortID,
				st.Render(fmt.Sprintf("%-14s", job.State)),
				job.ProjectName,
				fmt.Sprintf("%d/%d", job.Iteration, job.MaxIterations),
				dimStyle.Render(updated),
			)

			if i == m.cursor {
				line = selectedStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  j/k navigate  enter details  r refresh  q quit"))
	return b.String()
}

// ── Level 2: Job Detail + Session List ──────────────────────────────────────

func (m Model) detailView() string {
	var b strings.Builder
	w := m.w()
	job := m.selected

	st, ok := stateStyle[job.State]
	if !ok {
		st = dimStyle
	}

	b.WriteString(titleStyle.Render("JOB"))
	b.WriteString(dimStyle.Render("  " + job.ID))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	// Key-value pairs in two columns.
	kv := func(k, v string) {
		b.WriteString(fmt.Sprintf("  %s %s\n", headerStyle.Render(fmt.Sprintf("%-11s", k)), v))
	}
	kv("State", st.Render(job.State))
	kv("Project", job.ProjectName)
	kv("Issue", job.FixFlowIssueID)
	kv("Iteration", fmt.Sprintf("%d/%d", job.Iteration, job.MaxIterations))
	if job.BranchName != "" {
		kv("Branch", job.BranchName)
	}
	if job.CommitSHA != "" {
		kv("Commit", job.CommitSHA[:minInt(12, len(job.CommitSHA))])
	}
	if job.ErrorMessage != "" {
		kv("Error", lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(job.ErrorMessage))
	}
	if job.RejectReason != "" {
		kv("Rejected", job.RejectReason)
	}

	// Session pipeline table.
	b.WriteString("\n")
	b.WriteString(titleStyle.Render("PIPELINE"))
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d sessions", len(m.sessions))))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	if len(m.sessions) == 0 {
		b.WriteString(dimStyle.Render("  (no sessions yet)"))
		b.WriteString("\n")
	} else {
		b.WriteString(headerStyle.Render(fmt.Sprintf("  %-3s %-14s %-10s %-8s %-16s %s",
			"#", "STEP", "STATUS", "PROVIDER", "TOKENS", "TIME")))
		b.WriteString("\n")

		for i, s := range m.sessions {
			cursor := "  "
			if i == m.sessCursor {
				cursor = "> "
			}

			sst, ok := sessStatusStyle[s.Status]
			if !ok {
				sst = dimStyle
			}

			tokens := fmt.Sprintf("%d/%d", s.InputTokens, s.OutputTokens)
			dur := fmt.Sprintf("%ds", s.DurationMS/1000)

			line := fmt.Sprintf("%s%-3d %-14s %s %-8s %-16s %s",
				cursor,
				i+1,
				s.Step,
				sst.Render(fmt.Sprintf("%-10s", s.Status)),
				s.LLMProvider,
				tokens,
				dimStyle.Render(dur),
			)

			if i == m.sessCursor {
				line = selectedStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")
	hints := "  j/k navigate  enter view session  esc back  r refresh  q quit"
	if job.WorktreePath != "" {
		hints = "  j/k navigate  enter view session  d diff  esc back  r refresh  q quit"
	}
	b.WriteString(dimStyle.Render(hints))
	return b.String()
}

// ── Level 3: Session Detail ─────────────────────────────────────────────────

func (m Model) sessionView() string {
	var b strings.Builder
	w := m.w()
	sess := m.selectedSession

	// Find session number from sessions list.
	sessNum := 0
	for i, s := range m.sessions {
		if s.ID == sess.ID {
			sessNum = i + 1
			break
		}
	}

	// Title with session number.
	b.WriteString(titleStyle.Render(fmt.Sprintf("SESSION #%d", sessNum)))
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %s (iter %d)", sess.Step, sess.Iteration)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	// Metadata.
	sst, ok := sessStatusStyle[sess.Status]
	if !ok {
		sst = dimStyle
	}
	kv := func(k, v string) {
		b.WriteString(fmt.Sprintf("  %s %s\n", headerStyle.Render(fmt.Sprintf("%-11s", k)), v))
	}
	kv("Status", sst.Render(sess.Status))
	kv("Provider", sess.LLMProvider)
	kv("Tokens", fmt.Sprintf("%d in / %d out", sess.InputTokens, sess.OutputTokens))
	kv("Duration", fmt.Sprintf("%ds", sess.DurationMS/1000))
	if sess.ErrorMessage != "" {
		kv("Error", lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(sess.ErrorMessage))
	}

	// Tab bar for input/output toggle.
	b.WriteString("\n")
	inputTab := inactiveTab.Render("  INPUT ")
	outputTab := inactiveTab.Render("  OUTPUT ")
	if m.showInput {
		inputTab = activeTab.Render("  INPUT ")
	} else {
		outputTab = activeTab.Render("  OUTPUT ")
	}
	b.WriteString(inputTab)
	b.WriteString(dimStyle.Render(" │ "))
	b.WriteString(outputTab)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	// Scrollable body.
	avail := m.scrollHeight()
	start, end := scrollWindow(m.lines, m.scrollOffset, avail)
	for _, line := range m.lines[start:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Footer.
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")
	scrollInfo := scrollPercent(m.lines, m.scrollOffset, avail)
	b.WriteString(dimStyle.Render(fmt.Sprintf("  j/k scroll  d/u half-page  tab toggle  esc back  q quit%s", scrollInfo)))
	return b.String()
}

// ── Diff View ───────────────────────────────────────────────────────────────

func (m Model) diffView() string {
	var b strings.Builder
	w := m.w()

	b.WriteString(titleStyle.Render("DIFF"))
	if m.selected != nil {
		b.WriteString(dimStyle.Render("  " + m.selected.ID))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	avail := m.scrollHeight()
	start, end := scrollWindow(m.diffLines, m.diffOffset, avail)
	for _, line := range m.diffLines[start:end] {
		b.WriteString(colorDiffLine(line))
		b.WriteString("\n")
	}

	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")
	scrollInfo := scrollPercent(m.diffLines, m.diffOffset, avail)
	b.WriteString(dimStyle.Render(fmt.Sprintf("  j/k scroll  d/u half-page  esc back  q quit%s", scrollInfo)))
	return b.String()
}

func colorDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
		return diffMetaStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return diffAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return diffDelStyle.Render(line)
	case strings.HasPrefix(line, "@@"):
		return diffHunkStyle.Render(line)
	case strings.HasPrefix(line, "diff --git"):
		return diffMetaStyle.Render(line)
	default:
		return line
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func (m Model) w() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

func (m Model) scrollHeight() int {
	// Reserve ~13 lines for chrome (title, separator, metadata, tabs, footer).
	h := m.height - 13
	if h < 1 {
		h = 1
	}
	return h
}

func scrollWindow(lines []string, offset, avail int) (int, int) {
	if avail < 1 {
		avail = 1
	}
	start := offset
	if start > len(lines) {
		start = len(lines)
	}
	end := start + avail
	if end > len(lines) {
		end = len(lines)
	}
	return start, end
}

func scrollPercent(lines []string, offset, avail int) string {
	if len(lines) <= avail {
		return ""
	}
	max := len(lines) - avail
	if max <= 0 {
		return ""
	}
	pct := offset * 100 / max
	return fmt.Sprintf("  [%d%%]", pct)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
