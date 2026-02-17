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
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
)

type Model struct {
	store    *db.Store
	jobs     []db.Job
	cursor   int
	selected *db.Job
	detail   string
	err      error
	width    int
	height   int
}

func NewModel(store *db.Store) Model {
	return Model{store: store}
}

type jobsMsg []db.Job
type errMsg error

func (m Model) Init() tea.Cmd {
	return m.fetchJobs
}

func (m Model) fetchJobs() tea.Msg {
	jobs, err := m.store.ListJobs(context.Background(), "", "all")
	if err != nil {
		return errMsg(err)
	}
	return jobsMsg(jobs)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
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
				m.detail = m.buildDetail(m.selected)
			}
		case "esc":
			m.selected = nil
			m.detail = ""
		case "r":
			return m, m.fetchJobs
		}
	case jobsMsg:
		m.jobs = msg
		m.err = nil
	case errMsg:
		m.err = msg
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}

	if m.selected != nil {
		return m.detailView()
	}

	return m.listView()
}

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

func (m Model) detailView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Job Details"))
	b.WriteString("\n\n")
	b.WriteString(m.detail)
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("esc: back  q: quit"))
	return b.String()
}

func (m Model) buildDetail(job *db.Job) string {
	style, ok := stateStyle[job.State]
	if !ok {
		style = dimStyle
	}

	var b strings.Builder
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

	// Load sessions.
	sessions, err := m.store.ListSessionsByJob(context.Background(), job.ID)
	if err == nil && len(sessions) > 0 {
		b.WriteString("\nSessions:\n")
		for _, s := range sessions {
			b.WriteString(fmt.Sprintf("  %s (iter %d) [%s] %s — %d in/%d out, %dms\n",
				s.Step, s.Iteration, s.LLMProvider, s.Status,
				s.InputTokens, s.OutputTokens, s.DurationMS))
		}
	}

	return b.String()
}
