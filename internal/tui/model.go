package tui

import (
	"context"
	"fmt"
	"strings"

	"fixflow/internal/db"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
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
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
)

// Model is the BubbleTea model for the FixFlow TUI.
// Navigation depth is determined by pointer-nil checks:
//   - selected == nil                              → Level 1 (job list)
//   - selected != nil && selectedSession == nil     → Level 2 (job detail + session list)
//   - selectedSession != nil                        → Level 3 (session detail)
type Model struct {
	store *db.Store

	// Level 1: job list
	jobs   []db.Job
	cursor int

	// Level 2: job detail + session list
	selected   *db.Job
	sessions   []db.LLMSessionSummary
	sessCursor int

	// Level 3: session detail with scrollable output
	selectedSession *db.LLMSession
	scrollOffset    int
	lines           []string // pre-split response_text lines

	err    error
	width  int
	height int
}

func NewModel(store *db.Store) Model {
	return Model{store: store}
}

// Message types.
type jobsMsg []db.Job
type sessionsMsg []db.LLMSessionSummary
type sessionMsg db.LLMSession
type errMsg error

func (m Model) Init() tea.Cmd {
	return m.fetchJobs
}

// Commands — all DB calls happen here, never in View().

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
		m.scrollOffset = 0
		if sess.ResponseText != "" {
			m.lines = strings.Split(sess.ResponseText, "\n")
		} else if sess.Status == "running" {
			m.lines = []string{"(in progress)"}
		} else {
			m.lines = []string{"(no output)"}
		}
		m.err = nil

	case errMsg:
		m.err = msg

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global keys.
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	}

	// Level 3: session detail (scrollable).
	if m.selectedSession != nil {
		return m.handleKeyLevel3(key)
	}

	// Level 2: job detail + session list.
	if m.selected != nil {
		return m.handleKeyLevel2(key)
	}

	// Level 1: job list.
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
	availHeight := m.availableScrollHeight()

	switch key {
	case "up", "k":
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
	case "down", "j":
		maxOffset := len(m.lines) - availHeight
		if maxOffset < 0 {
			maxOffset = 0
		}
		if m.scrollOffset < maxOffset {
			m.scrollOffset++
		}
	case "u":
		m.scrollOffset -= availHeight / 2
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}
	case "d":
		maxOffset := len(m.lines) - availHeight
		if maxOffset < 0 {
			maxOffset = 0
		}
		m.scrollOffset += availHeight / 2
		if m.scrollOffset > maxOffset {
			m.scrollOffset = maxOffset
		}
	case "esc":
		m.selectedSession = nil
		m.lines = nil
		m.scrollOffset = 0
	}
	return m, nil
}

// View dispatches on navigation depth.
func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}
	if m.selectedSession != nil {
		return m.sessionView()
	}
	if m.selected != nil {
		return m.detailView()
	}
	return m.listView()
}

// Level 1: job list.
func (m Model) listView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("FixFlow Jobs"))
	b.WriteString("\n\n")

	if len(m.jobs) == 0 {
		b.WriteString(dimStyle.Render("No jobs found."))
		b.WriteString("\n")
	}

	for i, job := range m.jobs {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		style, ok := stateStyle[job.State]
		if !ok {
			style = dimStyle
		}
		line := fmt.Sprintf("%s%-16s %s %s",
			cursor,
			job.ID,
			style.Render(fmt.Sprintf("%-14s", job.State)),
			job.ProjectName,
		)

		if i == m.cursor {
			line = selectedStyle.Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("j/k: navigate  enter: details  r: refresh  q: quit"))
	return b.String()
}

// Level 2: job detail + selectable session list.
func (m Model) detailView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Job Details"))
	b.WriteString("\n\n")

	job := m.selected
	style, ok := stateStyle[job.State]
	if !ok {
		style = dimStyle
	}

	b.WriteString(fmt.Sprintf("ID:         %s\n", job.ID))
	b.WriteString(fmt.Sprintf("State:      %s\n", style.Render(job.State)))
	b.WriteString(fmt.Sprintf("Project:    %s\n", job.ProjectName))
	b.WriteString(fmt.Sprintf("Issue:      %s\n", job.FixFlowIssueID))
	b.WriteString(fmt.Sprintf("Iteration:  %d/%d\n", job.Iteration, job.MaxIterations))
	if job.BranchName != "" {
		b.WriteString(fmt.Sprintf("Branch:     %s\n", job.BranchName))
	}
	if job.CommitSHA != "" {
		b.WriteString(fmt.Sprintf("Commit:     %s\n", job.CommitSHA))
	}
	if job.MRURL != "" {
		b.WriteString(fmt.Sprintf("MR:         %s\n", job.MRURL))
	}
	if job.ErrorMessage != "" {
		b.WriteString(fmt.Sprintf("Error:      %s\n", job.ErrorMessage))
	}
	if job.RejectReason != "" {
		b.WriteString(fmt.Sprintf("Rejected:   %s\n", job.RejectReason))
	}
	b.WriteString(fmt.Sprintf("Created:    %s\n", job.CreatedAt))
	b.WriteString(fmt.Sprintf("Updated:    %s\n", job.UpdatedAt))

	// Session list.
	b.WriteString("\n")
	b.WriteString(titleStyle.Render("Sessions"))
	b.WriteString("\n\n")

	if len(m.sessions) == 0 {
		b.WriteString(dimStyle.Render("  (no sessions yet)"))
		b.WriteString("\n")
	}

	for i, s := range m.sessions {
		cursor := "  "
		if i == m.sessCursor {
			cursor = "> "
		}

		statusStyle, ok := sessStatusStyle[s.Status]
		if !ok {
			statusStyle = dimStyle
		}

		line := fmt.Sprintf("%s%-13s iter %d  %s  %-6s  %d/%d tokens  %ds",
			cursor,
			s.Step,
			s.Iteration,
			statusStyle.Render(fmt.Sprintf("%-9s", s.Status)),
			s.LLMProvider,
			s.InputTokens,
			s.OutputTokens,
			s.DurationMS/1000,
		)

		if i == m.sessCursor {
			line = selectedStyle.Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("j/k: navigate  enter: view session  esc: back  r: refresh  q: quit"))
	return b.String()
}

// Level 3: session detail with scrollable output.
func (m Model) sessionView() string {
	var b strings.Builder

	sess := m.selectedSession

	// Breadcrumb title.
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s > %s (iter %d)", sess.JobID, sess.Step, sess.Iteration)))
	b.WriteString("\n\n")

	// Summary header.
	statusStyle, ok := sessStatusStyle[sess.Status]
	if !ok {
		statusStyle = dimStyle
	}

	b.WriteString(fmt.Sprintf("Step:       %s\n", sess.Step))
	b.WriteString(fmt.Sprintf("Status:     %s\n", statusStyle.Render(sess.Status)))
	b.WriteString(fmt.Sprintf("Provider:   %s\n", sess.LLMProvider))
	b.WriteString(fmt.Sprintf("Tokens:     %d in / %d out\n", sess.InputTokens, sess.OutputTokens))
	b.WriteString(fmt.Sprintf("Duration:   %ds\n", sess.DurationMS/1000))
	if sess.ErrorMessage != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		b.WriteString(fmt.Sprintf("Error:      %s\n", errStyle.Render(sess.ErrorMessage)))
	}

	// Separator.
	w := m.width
	if w == 0 {
		w = 80
	}
	b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	// Scrollable body.
	availHeight := m.availableScrollHeight()
	if availHeight < 1 {
		availHeight = 1
	}

	end := m.scrollOffset + availHeight
	if end > len(m.lines) {
		end = len(m.lines)
	}
	start := m.scrollOffset
	if start > len(m.lines) {
		start = len(m.lines)
	}
	for _, line := range m.lines[start:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Footer.
	b.WriteString("\n")
	scrollInfo := ""
	if len(m.lines) > availHeight {
		pct := 0
		maxOffset := len(m.lines) - availHeight
		if maxOffset > 0 {
			pct = m.scrollOffset * 100 / maxOffset
		}
		scrollInfo = fmt.Sprintf("  [%d%%]", pct)
	}
	b.WriteString(dimStyle.Render(fmt.Sprintf("j/k: scroll  d/u: half-page  esc: back  q: quit%s", scrollInfo)))
	return b.String()
}

// availableScrollHeight returns the number of lines available for the scrollable body in Level 3.
func (m Model) availableScrollHeight() int {
	// Header: title(1) + blank(1) + fields(5-6) + separator(1) = ~9 lines
	// Footer: blank(1) + hints(1) = 2 lines
	// Conservative: 11 lines of chrome.
	h := m.height - 11
	if h < 1 {
		h = 1
	}
	return h
}
