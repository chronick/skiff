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
	viewDetail
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

	footerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	connOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("#73F59F")).Bold(true)
	connErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Bold(true)
	msgStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD93D"))
	statsStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB"))
	detailKey    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#888888"))
	detailVal    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA"))
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5B5FC7"))
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
	showStats       bool
	containerLogSrc bool
	containerLogRaw string
}

// listLen returns the total number of selectable items (resources + schedules).
func (m model) listLen() int {
	return len(m.resources) + len(m.schedules)
}

// selectedName returns the name of the currently selected item.
func (m model) selectedName() string {
	if m.cursor < len(m.resources) {
		if m.cursor >= 0 && m.cursor < len(m.resources) {
			return m.resources[m.cursor].Name
		}
	} else {
		idx := m.cursor - len(m.resources)
		if idx >= 0 && idx < len(m.schedules) {
			return m.schedules[idx].Name
		}
	}
	return ""
}

// selectedIsSchedule returns true if cursor is on a schedule.
func (m model) selectedIsSchedule() bool {
	return m.cursor >= len(m.resources)
}

// selectedIsContainer returns true if cursor is on a container resource.
func (m model) selectedIsContainer() bool {
	if m.cursor >= 0 && m.cursor < len(m.resources) {
		return m.resources[m.cursor].Type == "container"
	}
	return false
}

// selectedResource returns the resource at cursor, or nil.
func (m model) selectedResource() *client.ResourceInfo {
	if m.cursor >= 0 && m.cursor < len(m.resources) {
		r := m.resources[m.cursor]
		return &r
	}
	return nil
}

// selectedSchedule returns the schedule at cursor, or nil.
func (m model) selectedSchedule() *client.ScheduleInfo {
	idx := m.cursor - len(m.resources)
	if idx >= 0 && idx < len(m.schedules) {
		s := m.schedules[idx]
		return &s
	}
	return nil
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
		case "run-now":
			err = c.RunNow(name)
		case "build":
			var names []string
			if name != "" {
				names = []string{name}
			}
			_, err = c.Build(names)
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
			// Clamp cursor
			if max := m.listLen() - 1; max >= 0 && m.cursor > max {
				m.cursor = max
			}
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
			verb := msg.action
			switch msg.action {
			case "start":
				verb = "started"
			case "stop":
				verb = "stopped"
			case "restart":
				verb = "restarted"
			case "run-now":
				verb = "triggered"
			case "build":
				verb = "built"
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
		switch m.view {
		case viewDashboard:
			if m.cursor > 0 {
				m.cursor--
			}
		case viewLogs:
			if m.logOffset > 0 {
				m.logOffset--
			}
		case viewDetail:
			if m.logOffset > 0 {
				m.logOffset--
			}
		}

	case "down", "j":
		switch m.view {
		case viewDashboard:
			if max := m.listLen() - 1; m.cursor < max {
				m.cursor++
			}
		case viewLogs:
			m.logOffset++
		case viewDetail:
			m.logOffset++
		}

	case "enter", "l":
		if m.view == viewDashboard && m.listLen() > 0 {
			m.view = viewLogs
			m.logOffset = 0
			m.logs = nil
			m.containerLogRaw = ""
			m.containerLogSrc = false
			return m, fetchLogs(m.client, m.selectedName())
		}

	case "esc":
		switch m.view {
		case viewLogs:
			m.view = viewDashboard
			m.logs = nil
			m.containerLogRaw = ""
			m.containerLogSrc = false
		case viewStats:
			m.view = viewDashboard
		case viewDetail:
			m.view = viewDashboard
			m.logOffset = 0
		}

	case "i":
		if m.view == viewDashboard && m.listLen() > 0 {
			m.view = viewDetail
			m.logOffset = 0
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
		if m.view == viewLogs && m.selectedIsContainer() {
			m.containerLogSrc = !m.containerLogSrc
			m.logOffset = 0
			if m.containerLogSrc {
				return m, fetchContainerLogs(m.client, m.selectedName(), 200)
			}
			return m, fetchLogs(m.client, m.selectedName())
		}

	case "s":
		if m.view == viewDashboard && !m.selectedIsSchedule() && m.selectedName() != "" {
			name := m.selectedName()
			m.message = fmt.Sprintf("starting %s...", name)
			return m, doAction(m.client, "start", name)
		}

	case "x":
		if m.view == viewDashboard && !m.selectedIsSchedule() && m.selectedName() != "" {
			name := m.selectedName()
			m.message = fmt.Sprintf("stopping %s...", name)
			return m, doAction(m.client, "stop", name)
		}

	case "r":
		if m.view == viewDashboard && !m.selectedIsSchedule() && m.selectedName() != "" {
			name := m.selectedName()
			m.message = fmt.Sprintf("restarting %s...", name)
			return m, doAction(m.client, "restart", name)
		}

	case "n":
		if m.view == viewDashboard && m.selectedIsSchedule() {
			name := m.selectedName()
			m.message = fmt.Sprintf("triggering %s...", name)
			return m, doAction(m.client, "run-now", name)
		}

	case "b":
		if m.view == viewDashboard && m.selectedIsContainer() {
			name := m.selectedName()
			m.message = fmt.Sprintf("building %s...", name)
			return m, doAction(m.client, "build", name)
		}

	case "B":
		if m.view == viewDashboard {
			m.message = "building all containers..."
			return m, doAction(m.client, "build", "")
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

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	switch m.view {
	case viewLogs:
		return m.renderLogs()
	case viewStats:
		return m.renderStats()
	case viewDetail:
		return m.renderDetail()
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

	totalItems := m.listLen()

	// Resources
	if len(m.resources) == 0 && len(m.schedules) == 0 && !m.connected {
		b.WriteString(dimStyle.Render("  Waiting for daemon...") + "\n")
	} else if totalItems == 0 {
		b.WriteString(dimStyle.Render("  No resources configured") + "\n")
	} else {
		if len(m.resources) > 0 {
			if m.showStats {
				hdr := fmt.Sprintf("  %-22s %-12s %-12s %-8s %-8s %-8s %-14s %s", "NAME", "TYPE", "STATE", "HEALTH", "UPTIME", "CPU%", "MEM", "PIDS")
				b.WriteString(colHeaderStyle.Render(hdr) + "\n")
			} else {
				hdr := fmt.Sprintf("  %-22s %-12s %-12s %-8s %s", "NAME", "TYPE", "STATE", "HEALTH", "UPTIME")
				b.WriteString(colHeaderStyle.Render(hdr) + "\n")
			}

			for i, r := range m.resources {
				b.WriteString(m.renderResourceRow(i, r))
			}
		}

		// Schedules
		if len(m.schedules) > 0 {
			b.WriteString("\n")
			shdr := fmt.Sprintf("  %-22s %-14s %-14s %s", "SCHEDULE", "LAST RESULT", "DURATION", "NEXT RUN")
			b.WriteString(colHeaderStyle.Render(shdr) + "\n")

			for i, s := range m.schedules {
				globalIdx := len(m.resources) + i
				b.WriteString(m.renderScheduleRow(globalIdx, s))
			}
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

	// Footer - context-sensitive hints
	var footer string
	if m.selectedIsSchedule() {
		footer = " ↑↓ navigate │ ↵ logs │ i detail │ n run now │ t stats │ a all up │ d all down │ q quit"
	} else {
		footer = " ↑↓ navigate │ ↵ logs │ i detail │ s start │ x stop │ r restart │ b build │ t stats │ q quit"
	}
	if len(footer) > m.width {
		if m.selectedIsSchedule() {
			footer = " ↑↓ nav │ ↵ logs │ i info │ n run │ q quit"
		} else {
			footer = " ↑↓ nav │ ↵ logs │ i info │ s/x/r start/stop/restart │ q quit"
		}
	}
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func (m model) renderResourceRow(idx int, r client.ResourceInfo) string {
	cpuStr, memStr, pidsStr := "-", "-", "-"
	if m.showStats && r.Stats != nil {
		cpuStr = fmt.Sprintf("%.1f%%", r.Stats.CPUPercent)
		memStr = fmt.Sprintf("%d/%dMB", r.Stats.MemUsageMB, r.Stats.MemLimitMB)
		pidsStr = fmt.Sprintf("%d", r.Stats.PIDs)
	}

	if idx == m.cursor {
		var plain string
		if m.showStats {
			plain = fmt.Sprintf("  %-22s %-12s %-12s %-8s %-8s %-8s %-14s %s",
				r.Name, r.Type, r.State, healthText(r.Health), uptimeText(r.UptimeSecs), cpuStr, memStr, pidsStr)
		} else {
			plain = fmt.Sprintf("  %-22s %-12s %-12s %-8s %s",
				r.Name, r.Type, r.State, healthText(r.Health), uptimeText(r.UptimeSecs))
		}
		return selectedStyle.Render(padTo(plain, m.width)) + "\n"
	}

	nameCell := padCell(r.Name, 22)
	typeCell := padCell(r.Type, 12)
	stateCell := padCell(renderState(r.State), 12)
	healthCell := padCell(renderHealth(r.Health), 8)
	uptimeCell := padCell(renderUptime(r.UptimeSecs), 8)
	if m.showStats {
		cpuCell := padCell(statsStyle.Render(cpuStr), 8)
		memCell := padCell(statsStyle.Render(memStr), 14)
		pidsCell := statsStyle.Render(pidsStr)
		return fmt.Sprintf("  %s %s %s %s %s %s %s %s\n", nameCell, typeCell, stateCell, healthCell, uptimeCell, cpuCell, memCell, pidsCell)
	}
	return fmt.Sprintf("  %s %s %s %s %s\n", nameCell, typeCell, stateCell, healthCell, uptimeCell)
}

func (m model) renderScheduleRow(globalIdx int, s client.ScheduleInfo) string {
	durStr := "-"
	if s.Duration > 0 {
		durStr = fmtDuration(time.Duration(s.Duration) * time.Millisecond)
	}

	if globalIdx == m.cursor {
		plain := fmt.Sprintf("  %-22s %-14s %-14s %s",
			s.Name, schedResultText(s.LastResult), durStr, nextRunText(s.NextRun))
		return selectedStyle.Render(padTo(plain, m.width)) + "\n"
	}

	nameCell := padCell(s.Name, 22)
	resultCell := padCell(renderScheduleResult(s.LastResult), 14)
	durCell := padCell(durStr, 14)
	nextCell := renderNextRun(s.NextRun)
	return fmt.Sprintf("  %s %s %s %s\n", nameCell, resultCell, durCell, nextCell)
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

	var footer string
	if m.selectedIsContainer() {
		footer = " esc back │ ↑↓ scroll │ c toggle source │ q quit"
	} else {
		footer = " esc back │ ↑↓ scroll │ q quit"
	}
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

func (m model) renderDetail() string {
	var b strings.Builder

	title := titleStyle.Render(" ✈  plane ")
	name := m.selectedName()
	breadcrumb := dimStyle.Render(" > ") + lipgloss.NewStyle().Bold(true).Render(name) + dimStyle.Render(" detail")
	b.WriteString(title + breadcrumb + "\n\n")

	var detailLines []string

	if r := m.selectedResource(); r != nil {
		detailLines = m.resourceDetailLines(r)
	} else if s := m.selectedSchedule(); s != nil {
		detailLines = m.scheduleDetailLines(s)
	} else {
		detailLines = []string{"  No item selected"}
	}

	// Scrollable detail
	visible := m.height - 5
	if visible < 5 {
		visible = 5
	}
	start := m.logOffset
	if start > len(detailLines) {
		start = len(detailLines)
	}
	end := start + visible
	if end > len(detailLines) {
		end = len(detailLines)
	}
	for _, line := range detailLines[start:end] {
		b.WriteString(line + "\n")
	}

	// Fill to bottom
	content := b.String()
	lines := strings.Count(content, "\n")
	remaining := m.height - lines - 2
	if remaining > 0 {
		b.WriteString(strings.Repeat("\n", remaining))
	}

	footer := " esc back │ ↑↓ scroll │ q quit"
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func (m model) resourceDetailLines(r *client.ResourceInfo) []string {
	var lines []string

	lines = append(lines, "  "+sectionStyle.Render("General"))
	lines = append(lines, detailRow("  Name", r.Name))
	lines = append(lines, detailRow("  Type", r.Type))
	lines = append(lines, detailRow("  State", r.State))
	if r.PID > 0 {
		lines = append(lines, detailRow("  PID", fmt.Sprintf("%d", r.PID)))
	}
	if r.UptimeSecs > 0 {
		lines = append(lines, detailRow("  Uptime", fmtDuration(time.Duration(r.UptimeSecs)*time.Second)))
	}
	if !r.StartedAt.IsZero() {
		lines = append(lines, detailRow("  Started At", r.StartedAt.Format(time.RFC3339)))
	}
	if r.ExitCode != 0 {
		lines = append(lines, detailRow("  Exit Code", fmt.Sprintf("%d", r.ExitCode)))
	}
	lines = append(lines, detailRow("  Config Hash", r.ConfigHash))

	if r.LastError != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+sectionStyle.Render("Error"))
		lines = append(lines, "  "+stateFailed.Render(r.LastError))
	}

	if len(r.Ports) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+sectionStyle.Render("Ports"))
		for _, p := range r.Ports {
			lines = append(lines, "  "+detailVal.Render("• "+p))
		}
	}

	if len(r.DependsOn) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+sectionStyle.Render("Dependencies"))
		for _, dep := range r.DependsOn {
			lines = append(lines, "  "+detailVal.Render("• "+dep))
		}
	}

	if r.Health != nil {
		lines = append(lines, "")
		lines = append(lines, "  "+sectionStyle.Render("Health Check"))
		lines = append(lines, detailRow("  Status", r.Health.Status))
		lines = append(lines, detailRow("  Consecutive Fails", fmt.Sprintf("%d", r.Health.ConsecutiveFails)))
		if !r.Health.LastCheck.IsZero() {
			lines = append(lines, detailRow("  Last Check", r.Health.LastCheck.Format("15:04:05")))
		}
		if r.Health.LastError != "" {
			lines = append(lines, detailRow("  Last Error", r.Health.LastError))
		}
	}

	if r.Stats != nil {
		lines = append(lines, "")
		lines = append(lines, "  "+sectionStyle.Render("Container Stats"))
		lines = append(lines, detailRow("  CPU", fmt.Sprintf("%.1f%%", r.Stats.CPUPercent)))
		lines = append(lines, detailRow("  Memory", fmt.Sprintf("%d MB / %d MB", r.Stats.MemUsageMB, r.Stats.MemLimitMB)))
		lines = append(lines, detailRow("  PIDs", fmt.Sprintf("%d", r.Stats.PIDs)))
	}

	return lines
}

func (m model) scheduleDetailLines(s *client.ScheduleInfo) []string {
	var lines []string

	lines = append(lines, "  "+sectionStyle.Render("Schedule"))
	lines = append(lines, detailRow("  Name", s.Name))
	lines = append(lines, detailRow("  Last Result", s.LastResult))

	if s.LastRun != nil {
		lines = append(lines, detailRow("  Last Run", s.LastRun.Format(time.RFC3339)))
	} else {
		lines = append(lines, detailRow("  Last Run", "never"))
	}

	if !s.NextRun.IsZero() {
		until := time.Until(s.NextRun)
		nextStr := s.NextRun.Format("15:04:05")
		if until > 0 {
			nextStr += fmt.Sprintf(" (in %s)", fmtDuration(until))
		} else {
			nextStr += " (overdue)"
		}
		lines = append(lines, detailRow("  Next Run", nextStr))
	}

	if s.Duration > 0 {
		lines = append(lines, detailRow("  Last Duration", fmtDuration(time.Duration(s.Duration)*time.Millisecond)))
	}

	if s.LastError != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+sectionStyle.Render("Error"))
		lines = append(lines, "  "+stateFailed.Render(s.LastError))
	}

	return lines
}

func detailRow(key, value string) string {
	return "  " + detailKey.Render(key+":") + " " + detailVal.Render(value)
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

func schedResultText(result string) string {
	if result == "" {
		return "-"
	}
	return result
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

func nextRunText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	until := time.Until(t)
	if until <= 0 {
		return "overdue"
	}
	return "in " + fmtDuration(until)
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
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
