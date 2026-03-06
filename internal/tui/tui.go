package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chronick/plane/internal/client"
)

type viewMode int

const (
	viewDashboard viewMode = iota
	viewLogs
	viewStats
)

// Messages
type statusMsg struct {
	snap *client.StatusSnapshot
	err  error
}

type logsMsg struct {
	entries []client.LogEntry
	err     error
}

type containerLogsMsg struct {
	raw string
	err error
}

type statsListMsg struct {
	entries []client.StatsEntry
	err     error
}

type actionMsg struct {
	action string
	name   string
	err    error
}

type tickMsg time.Time

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#5B5FC7")).
			Padding(0, 1)

	colHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#888888"))

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#5B5FC7"))

	stateRunning  = lipgloss.NewStyle().Foreground(lipgloss.Color("#73F59F"))
	stateFailed   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	stateStopped  = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	stateStarting = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD93D"))

	healthHealthy   = lipgloss.NewStyle().Foreground(lipgloss.Color("#73F59F"))
	healthUnhealthy = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	healthUnknown   = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))

	logError = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	logWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD93D"))
	logTime  = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	logLevel = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	connOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("#73F59F")).Bold(true)
	connErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Bold(true)
	msgStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD93D"))
	statsStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB"))
)

type model struct {
	client    *client.Client
	resources []client.ResourceInfo
	schedules []client.ScheduleInfo
	logs      []client.LogEntry
	stats     []client.StatsEntry

	view            viewMode
	cursor          int
	logOffset       int
	width           int
	height          int
	connected       bool
	message         string
	showStats       bool   // toggle stats columns in dashboard
	containerLogSrc bool   // show container stdout instead of ring buffer
	containerLogRaw string // raw container log output
}

func Run(socketPath string) error {
	c := client.New(socketPath)
	m := model{client: c, view: viewDashboard}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), tick())
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchStatus(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		snap, err := c.Status()
		return statusMsg{snap: snap, err: err}
	}
}

func fetchLogs(c *client.Client, name string) tea.Cmd {
	return func() tea.Msg {
		entries, err := c.Logs(name, 200)
		return logsMsg{entries: entries, err: err}
	}
}

func fetchStats(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		entries, err := c.Stats()
		return statsListMsg{entries: entries, err: err}
	}
}

func fetchContainerLogs(c *client.Client, name string, lines int) tea.Cmd {
	return func() tea.Msg {
		raw, err := c.ContainerLogs(name, lines)
		return containerLogsMsg{raw: raw, err: err}
	}
}

func doAction(c *client.Client, action, name string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch action {
		case "start":
			var names []string
			if name != "" {
				names = []string{name}
			}
			_, err = c.Up(names)
		case "stop":
			var names []string
			if name != "" {
				names = []string{name}
			}
			_, err = c.Down(names)
		case "restart":
			err = c.Restart(name)
		}
		return actionMsg{action: action, name: name, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case statusMsg:
		if msg.err != nil {
			m.connected = false
		} else {
			m.connected = true
			m.resources = msg.snap.Resources
			m.schedules = msg.snap.Schedules
		}
		return m, nil

	case logsMsg:
		if msg.err == nil {
			m.logs = msg.entries
		}
		return m, nil

	case containerLogsMsg:
		if msg.err == nil {
			m.containerLogRaw = msg.raw
		}
		return m, nil

	case statsListMsg:
		if msg.err == nil {
			m.stats = msg.entries
		}
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("error: %s %s: %s", msg.action, msg.name, msg.err)
		} else {
			verb := msg.action + "ed"
			if msg.action == "stop" {
				verb = "stopped"
			}
			target := msg.name
			if target == "" {
				target = "all"
			}
			m.message = fmt.Sprintf("%s %s", verb, target)
		}
		return m, fetchStatus(m.client)

	case tickMsg:
		cmds := []tea.Cmd{tick(), fetchStatus(m.client)}
		if m.view == viewLogs && m.selectedName() != "" {
			if m.containerLogSrc {
				cmds = append(cmds, fetchContainerLogs(m.client, m.selectedName(), 200))
			} else {
				cmds = append(cmds, fetchLogs(m.client, m.selectedName()))
			}
		}
		if m.view == viewStats || m.showStats {
			cmds = append(cmds, fetchStats(m.client))
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		if m.view == viewDashboard && m.cursor > 0 {
			m.cursor--
		} else if m.view == viewLogs && m.logOffset > 0 {
			m.logOffset--
		}

	case "down", "j":
		if m.view == viewDashboard {
			max := len(m.resources) - 1
			if max < 0 {
				max = 0
			}
			if m.cursor < max {
				m.cursor++
			}
		} else if m.view == viewLogs {
			m.logOffset++
		}

	case "enter", "l":
		if m.view == viewDashboard && len(m.resources) > 0 {
			m.view = viewLogs
			m.logOffset = 0
			m.logs = nil
			return m, fetchLogs(m.client, m.selectedName())
		}

	case "esc":
		if m.view == viewLogs {
			m.view = viewDashboard
			m.logs = nil
			m.containerLogRaw = ""
			m.containerLogSrc = false
		} else if m.view == viewStats {
			m.view = viewDashboard
		}

	case "t":
		if m.view == viewDashboard {
			m.showStats = !m.showStats
			if m.showStats {
				return m, fetchStats(m.client)
			}
		}

	case "S":
		if m.view == viewDashboard {
			m.view = viewStats
			return m, fetchStats(m.client)
		}

	case "c":
		if m.view == viewLogs && m.selectedName() != "" {
			m.containerLogSrc = !m.containerLogSrc
			if m.containerLogSrc {
				return m, fetchContainerLogs(m.client, m.selectedName(), 200)
			}
			return m, fetchLogs(m.client, m.selectedName())
		}

	case "s":
		if m.view == viewDashboard && len(m.resources) > 0 {
			name := m.selectedName()
			m.message = fmt.Sprintf("starting %s...", name)
			return m, doAction(m.client, "start", name)
		}

	case "x":
		if m.view == viewDashboard && len(m.resources) > 0 {
			name := m.selectedName()
			m.message = fmt.Sprintf("stopping %s...", name)
			return m, doAction(m.client, "stop", name)
		}

	case "r":
		if m.view == viewDashboard && len(m.resources) > 0 {
			name := m.selectedName()
			m.message = fmt.Sprintf("restarting %s...", name)
			return m, doAction(m.client, "restart", name)
		}

	case "a":
		if m.view == viewDashboard {
			m.message = "starting all..."
			return m, doAction(m.client, "start", "")
		}

	case "d":
		if m.view == viewDashboard {
			m.message = "stopping all..."
			return m, doAction(m.client, "stop", "")
		}
	}

	return m, nil
}

func (m model) selectedName() string {
	if m.cursor >= 0 && m.cursor < len(m.resources) {
		return m.resources[m.cursor].Name
	}
	return ""
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	switch m.view {
	case viewLogs:
		return m.renderLogs()
	case viewStats:
		return m.renderStats()
	default:
		return m.renderDashboard()
	}
}

func (m model) renderDashboard() string {
	var b strings.Builder

	// Header
	title := titleStyle.Render(" ✈  plane ")
	var status string
	if m.connected {
		status = connOK.Render("● connected")
	} else {
		status = connErr.Render("● disconnected")
	}
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(status)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(title + strings.Repeat(" ", gap) + status + "\n\n")

	// Resources
	if len(m.resources) == 0 && !m.connected {
		b.WriteString(dimStyle.Render("  Waiting for daemon...") + "\n")
	} else if len(m.resources) == 0 {
		b.WriteString(dimStyle.Render("  No resources configured") + "\n")
	} else {
		if m.showStats {
			hdr := fmt.Sprintf("  %-22s %-12s %-12s %-8s %-8s %-8s %-14s %s", "NAME", "TYPE", "STATE", "HEALTH", "UPTIME", "CPU%", "MEM", "PIDS")
			b.WriteString(colHeaderStyle.Render(hdr) + "\n")
		} else {
			hdr := fmt.Sprintf("  %-22s %-12s %-12s %-8s %s", "NAME", "TYPE", "STATE", "HEALTH", "UPTIME")
			b.WriteString(colHeaderStyle.Render(hdr) + "\n")
		}

		for i, r := range m.resources {
			cpuStr, memStr, pidsStr := "-", "-", "-"
			if m.showStats {
				if r.Stats != nil {
					cpuStr = fmt.Sprintf("%.1f%%", r.Stats.CPUPercent)
					memStr = fmt.Sprintf("%d/%dMB", r.Stats.MemUsageMB, r.Stats.MemLimitMB)
					pidsStr = fmt.Sprintf("%d", r.Stats.PIDs)
				}
			}

			if i == m.cursor {
				var plain string
				if m.showStats {
					plain = fmt.Sprintf("  %-22s %-12s %-12s %-8s %-8s %-8s %-14s %s",
						r.Name, r.Type, r.State, healthText(r.Health), uptimeText(r.UptimeSecs), cpuStr, memStr, pidsStr)
				} else {
					plain = fmt.Sprintf("  %-22s %-12s %-12s %-8s %s",
						r.Name, r.Type, r.State, healthText(r.Health), uptimeText(r.UptimeSecs))
				}
				line := selectedStyle.Render(padTo(plain, m.width))
				b.WriteString(line + "\n")
			} else {
				nameCell := padCell(r.Name, 22)
				typeCell := padCell(r.Type, 12)
				stateCell := padCell(renderState(r.State), 12)
				healthCell := padCell(renderHealth(r.Health), 8)
				uptimeCell := padCell(renderUptime(r.UptimeSecs), 8)
				if m.showStats {
					cpuCell := padCell(statsStyle.Render(cpuStr), 8)
					memCell := padCell(statsStyle.Render(memStr), 14)
					pidsCell := statsStyle.Render(pidsStr)
					b.WriteString(fmt.Sprintf("  %s %s %s %s %s %s %s %s\n", nameCell, typeCell, stateCell, healthCell, uptimeCell, cpuCell, memCell, pidsCell))
				} else {
					b.WriteString(fmt.Sprintf("  %s %s %s %s %s\n", nameCell, typeCell, stateCell, healthCell, uptimeCell))
				}
			}
		}
	}

	// Schedules
	if len(m.schedules) > 0 {
		b.WriteString("\n")
		shdr := fmt.Sprintf("  %-22s %-14s %s", "SCHEDULE", "LAST RESULT", "NEXT RUN")
		b.WriteString(colHeaderStyle.Render(shdr) + "\n")

		for _, s := range m.schedules {
			resultCell := padCell(renderScheduleResult(s.LastResult), 14)
			nextCell := renderNextRun(s.NextRun)
			b.WriteString(fmt.Sprintf("  %-22s %s %s\n", s.Name, resultCell, nextCell))
		}
	}

	// Status message
	if m.message != "" {
		b.WriteString("\n" + msgStyle.Render("  "+m.message) + "\n")
	}

	// Fill to bottom
	content := b.String()
	lines := strings.Count(content, "\n")
	remaining := m.height - lines - 2
	if remaining > 0 {
		b.WriteString(strings.Repeat("\n", remaining))
	}

	// Footer
	footer := " ↑↓ navigate │ enter logs │ s start │ x stop │ r restart │ t stats │ S stats view │ a all up │ d all down │ q quit"
	if len(footer) > m.width {
		footer = " ↑↓ nav │ ↵ logs │ s/x start/stop │ r restart │ t stats │ q quit"
	}
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func (m model) renderLogs() string {
	var b strings.Builder

	name := m.selectedName()
	title := titleStyle.Render(" ✈  plane ")
	srcLabel := "ring buffer"
	if m.containerLogSrc {
		srcLabel = "container stdout"
	}
	breadcrumb := dimStyle.Render(" > ") + lipgloss.NewStyle().Bold(true).Render(name) + dimStyle.Render(" logs") + dimStyle.Render(" ("+srcLabel+")")
	b.WriteString(title + breadcrumb + "\n\n")

	if m.containerLogSrc {
		// Render raw container logs
		if m.containerLogRaw == "" {
			b.WriteString(dimStyle.Render("  No container log output") + "\n")
		} else {
			visible := m.height - 5
			if visible < 5 {
				visible = 5
			}
			rawLines := strings.Split(strings.TrimRight(m.containerLogRaw, "\n"), "\n")
			start := len(rawLines) - visible - m.logOffset
			if start < 0 {
				start = 0
			}
			end := start + visible
			if end > len(rawLines) {
				end = len(rawLines)
			}
			for _, line := range rawLines[start:end] {
				b.WriteString("  " + line + "\n")
			}
		}
	} else {
		if len(m.logs) == 0 {
			b.WriteString(dimStyle.Render("  No log entries") + "\n")
		} else {
			visible := m.height - 5
			if visible < 5 {
				visible = 5
			}

			start := len(m.logs) - visible - m.logOffset
			if start < 0 {
				start = 0
			}
			end := start + visible
			if end > len(m.logs) {
				end = len(m.logs)
			}

			for _, e := range m.logs[start:end] {
				ts := logTime.Render(e.Timestamp.Format("15:04:05"))
				var lvl string
				switch e.Level {
				case "error":
					lvl = logError.Render("[ERROR]")
				case "warn":
					lvl = logWarn.Render("[WARN] ")
				default:
					lvl = logLevel.Render(fmt.Sprintf("[%-5s]", strings.ToUpper(e.Level)))
				}
				b.WriteString(fmt.Sprintf("  %s %s %s\n", ts, lvl, e.Message))
			}
		}
	}

	// Fill to bottom
	content := b.String()
	lines := strings.Count(content, "\n")
	remaining := m.height - lines - 2
	if remaining > 0 {
		b.WriteString(strings.Repeat("\n", remaining))
	}

	footer := " esc back │ ↑↓ scroll │ c toggle source │ q quit"
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func (m model) renderStats() string {
	var b strings.Builder

	title := titleStyle.Render(" ✈  plane ")
	breadcrumb := dimStyle.Render(" > ") + lipgloss.NewStyle().Bold(true).Render("container stats")
	b.WriteString(title + breadcrumb + "\n\n")

	if len(m.stats) == 0 {
		b.WriteString(dimStyle.Render("  No container stats available") + "\n")
	} else {
		hdr := fmt.Sprintf("  %-22s %-10s %-18s %s", "NAME", "CPU%", "MEM USAGE/LIMIT", "PIDS")
		b.WriteString(colHeaderStyle.Render(hdr) + "\n")

		for _, s := range m.stats {
			cpuStr := statsStyle.Render(fmt.Sprintf("%.1f%%", s.CPUPercent))
			memStr := statsStyle.Render(fmt.Sprintf("%dMB/%dMB", s.MemUsageMB, s.MemLimitMB))
			pidsStr := statsStyle.Render(fmt.Sprintf("%d", s.PIDs))
			b.WriteString(fmt.Sprintf("  %-22s %s %s %s\n",
				s.Name,
				padCell(cpuStr, 10),
				padCell(memStr, 18),
				pidsStr))
		}
	}

	// Fill to bottom
	content := b.String()
	lines := strings.Count(content, "\n")
	remaining := m.height - lines - 2
	if remaining > 0 {
		b.WriteString(strings.Repeat("\n", remaining))
	}

	footer := " esc back │ q quit"
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

// Render helpers

func renderState(state string) string {
	switch state {
	case "running":
		return stateRunning.Render("running")
	case "failed":
		return stateFailed.Render("failed")
	case "stopped":
		return stateStopped.Render("stopped")
	case "starting":
		return stateStarting.Render("starting")
	default:
		return dimStyle.Render(state)
	}
}

func renderHealth(h *client.HealthInfo) string {
	if h == nil {
		return dimStyle.Render("-")
	}
	switch h.Status {
	case "healthy":
		return healthHealthy.Render("●")
	case "unhealthy":
		return healthUnhealthy.Render("●")
	default:
		return healthUnknown.Render("○")
	}
}

func healthText(h *client.HealthInfo) string {
	if h == nil {
		return "-"
	}
	return h.Status
}

func renderUptime(secs int64) string {
	if secs <= 0 {
		return dimStyle.Render("-")
	}
	return fmtDuration(time.Duration(secs) * time.Second)
}

func uptimeText(secs int64) string {
	if secs <= 0 {
		return "-"
	}
	return fmtDuration(time.Duration(secs) * time.Second)
}

func renderScheduleResult(result string) string {
	switch result {
	case "success":
		return stateRunning.Render("success")
	case "failed":
		return stateFailed.Render("failed")
	case "running":
		return stateStarting.Render("running")
	default:
		return dimStyle.Render(result)
	}
}

func renderNextRun(t time.Time) string {
	if t.IsZero() {
		return dimStyle.Render("-")
	}
	until := time.Until(t)
	if until <= 0 {
		return stateStarting.Render("overdue")
	}
	return "in " + fmtDuration(until)
}

func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func padCell(styled string, width int) string {
	visible := lipgloss.Width(styled)
	if visible >= width {
		return styled
	}
	return styled + strings.Repeat(" ", width-visible)
}

func padTo(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
