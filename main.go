package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	gopsnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

const (
	nord0  = "#2E3440"
	nord1  = "#3B4252"
	nord2  = "#434C5E"
	nord3  = "#4C566A"
	nord4  = "#D8DEE9"
	nord5  = "#E5E9F0"
	nord6  = "#ECEFF4"
	nord7  = "#8FBCBB"
	nord8  = "#88C0D0"
	nord9  = "#81A1C1"
	nord10 = "#5E81AC"
	nord11 = "#BF616A"
	nord12 = "#D08770"
	nord13 = "#EBCB8B"
	nord14 = "#A3BE8C"
	nord15 = "#B48EAD"
)

var (
	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(nord3))

	styleLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color(nord4)).
			Bold(true)

	styleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color(nord3))

	styleTableHeader = lipgloss.NewStyle().
				Foreground(lipgloss.Color(nord7)).
				Bold(true)

	styleSelected = lipgloss.NewStyle().
			Background(lipgloss.Color(nord10)).
			Foreground(lipgloss.Color(nord6)).
			Bold(true)

	styleTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(nord9)).
			Bold(true)

	styleUptime = lipgloss.NewStyle().
			Foreground(lipgloss.Color(nord15))

	styleTime = lipgloss.NewStyle().
			Foreground(lipgloss.Color(nord13))

	styleConfirmBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(nord11)).
				Padding(1, 3)

	styleKillYes = lipgloss.NewStyle().
			Background(lipgloss.Color(nord11)).
			Foreground(lipgloss.Color(nord6)).
			Bold(true).
			Padding(0, 2)

	styleKillNo = lipgloss.NewStyle().
			Background(lipgloss.Color(nord3)).
			Foreground(lipgloss.Color(nord4)).
			Bold(true).
			Padding(0, 2)

	styleStatusOk  = lipgloss.NewStyle().Foreground(lipgloss.Color(nord14)).Bold(true)
	styleStatusErr = lipgloss.NewStyle().Foreground(lipgloss.Color(nord11)).Bold(true)
)

type ProcInfo struct {
	PID  int32
	Name string
	CPU  float64
	Mem  float32
}

type NetStats struct {
	BytesSent uint64
	BytesRecv uint64
}

type confirmState int

const (
	confirmNone confirmState = iota
	confirmKill
)

type Model struct {
	cpuPct       float64
	cpuCores     int
	ramUsed      float64
	ramTotal     float64
	ramPct       float64
	diskUsed     float64
	diskTotal    float64
	diskPct      float64
	temp         float64
	netUp        float64
	netDown      float64
	uptime       string
	procs        []ProcInfo
	prevNet      NetStats
	lastNet      time.Time
	width        int
	height       int
	cursor       int
	scrollOffset int
	confirm      confirmState
	confirmSel   int
	statusMsg    string
	statusIsErr  bool
	statusTimer  int
}

type tickMsg time.Time
type killDoneMsg struct {
	pid int32
	err error
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	return tick()
}

func (m Model) visibleRows() int {
	h := m.height
	if h == 0 {
		h = 40
	}
	panelH := h - 6
	v := panelH - 7
	if v < 1 {
		v = 1
	}
	return v
}

func doKill(pid int32) tea.Cmd {
	return func() tea.Msg {
		err := syscall.Kill(int(pid), syscall.SIGTERM)
		return killDoneMsg{pid: pid, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		if m.statusTimer > 0 {
			m.statusTimer--
			if m.statusTimer == 0 {
				m.statusMsg = ""
			}
		}
		m = recolectarDatos(m)
		if m.cursor >= len(m.procs) && len(m.procs) > 0 {
			m.cursor = len(m.procs) - 1
		}
		return m, tick()

	case killDoneMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("✗ Error al matar PID %d: %v", msg.pid, msg.err)
			m.statusIsErr = true
		} else {
			m.statusMsg = fmt.Sprintf("✓ Proceso %d terminado", msg.pid)
			m.statusIsErr = false
		}
		m.statusTimer = 4

	case tea.KeyMsg:
		if m.confirm == confirmKill {
			switch msg.String() {
			case "left", "h":
				m.confirmSel = 0
			case "right", "l":
				m.confirmSel = 1
			case "enter":
				if m.confirmSel == 0 && m.cursor < len(m.procs) {
					pid := m.procs[m.cursor].PID
					m.confirm = confirmNone
					return m, doKill(pid)
				}
				m.confirm = confirmNone
			case "esc", "n":
				m.confirm = confirmNone
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.scrollOffset {
					m.scrollOffset = m.cursor
				}
			}
		case "down", "j":
			if m.cursor < len(m.procs)-1 {
				m.cursor++
				visible := m.visibleRows()
				if m.cursor >= m.scrollOffset+visible {
					m.scrollOffset = m.cursor - visible + 1
				}
			}
		case "K", "x":
			if len(m.procs) > 0 {
				m.confirm = confirmKill
				m.confirmSel = 1
			}
		case "g":
			m.cursor = 0
			m.scrollOffset = 0
		case "G":
			m.cursor = len(m.procs) - 1
			visible := m.visibleRows()
			if m.cursor >= visible {
				m.scrollOffset = m.cursor - visible + 1
			}
		}
	}
	return m, nil
}

func barra(pct float64, ancho int, color string) string {
	llenos := int(math.Round(pct / 100 * float64(ancho)))
	if llenos > ancho {
		llenos = ancho
	}
	filled := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(strings.Repeat("█", llenos))
	empty := lipgloss.NewStyle().Foreground(lipgloss.Color(nord3)).Render(strings.Repeat("░", ancho-llenos))
	return "[" + filled + empty + "]"
}

func colorPct(pct float64, normal string) string {
	if pct >= 80 {
		return nord11
	} else if pct >= 50 {
		return nord12
	}
	return normal
}

func fmtBytes(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

func formatUptime(seconds uint64) string {
	h := seconds / 3600
	mn := (seconds % 3600) / 60
	if h >= 24 {
		d := h / 24
		h = h % 24
		return fmt.Sprintf("%dd %dh %dm", d, h, mn)
	}
	return fmt.Sprintf("%dh %dm", h, mn)
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func recolectarDatos(m Model) Model {
	pcts, err := cpu.Percent(0, false)
	if err == nil && len(pcts) > 0 {
		m.cpuPct = pcts[0]
	}
	if m.cpuCores == 0 {
		cores, _ := cpu.Counts(true)
		m.cpuCores = cores
	}

	ram, err := mem.VirtualMemory()
	if err == nil {
		m.ramUsed = float64(ram.Used) / 1024 / 1024
		m.ramTotal = float64(ram.Total) / 1024 / 1024
		m.ramPct = ram.UsedPercent
	}

	d, err := disk.Usage("/")
	if err == nil {
		m.diskUsed = float64(d.Used) / 1024 / 1024 / 1024
		m.diskTotal = float64(d.Total) / 1024 / 1024 / 1024
		m.diskPct = d.UsedPercent
	}

	temps, _ := host.SensorsTemperatures()
	for _, t := range temps {
		if t.Temperature > 0 && t.Temperature < 120 {
			m.temp = t.Temperature
			break
		}
	}

	netIO, err := gopsnet.IOCounters(false)
	if err == nil && len(netIO) > 0 {
		now := time.Now()
		if !m.lastNet.IsZero() {
			elapsed := now.Sub(m.lastNet).Seconds()
			m.netUp = float64(netIO[0].BytesSent-m.prevNet.BytesSent) / elapsed / 1024
			m.netDown = float64(netIO[0].BytesRecv-m.prevNet.BytesRecv) / elapsed / 1024
		}
		m.prevNet = NetStats{BytesSent: netIO[0].BytesSent, BytesRecv: netIO[0].BytesRecv}
		m.lastNet = time.Now()
	}

	uptimeSec, err := host.Uptime()
	if err == nil {
		m.uptime = formatUptime(uptimeSec)
	}

	procs, err := process.Processes()
	if err == nil {
		var lista []ProcInfo
		for _, p := range procs {
			cpuP, _ := p.CPUPercent()
			memP, _ := p.MemoryPercent()
			name, _ := p.Name()
			lista = append(lista, ProcInfo{PID: p.Pid, Name: name, CPU: cpuP, Mem: memP})
		}
		sort.Slice(lista, func(i, j int) bool {
			return lista[i].CPU > lista[j].CPU
		})
		m.procs = lista
	}
	return m
}

func (m Model) View() string {
	w := m.width
	h := m.height
	if w == 0 { w = 120 }
	if h == 0 { h = 40 }

	innerW := w - 4
	leftW := 36
	rightW := innerW - leftW - 2
	if rightW < 20 { rightW = 20 }
	barW := leftW - 14
	if barW < 8 { barW = 8 }
	panelH := h - 6

	now := time.Now().Format("15:04:05")

	// HEADER
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		styleTitle.Render("  GTOP"),
		styleDim.Render("  •  "),
		styleTime.Render("⏰ "+now),
		styleDim.Render("  •  "),
		styleUptime.Render("⬆ "+m.uptime),
		styleDim.Render("  •  "),
		styleDim.Render("↑↓ navegar  K matar  q salir"),
	)
	headerBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(nord10)).
		Padding(0, 2).
		Width(innerW).
		Render(header)

	// LEFT PANEL
	cpuColor := colorPct(m.cpuPct, nord13)
	cpuSection := fmt.Sprintf("%s\n%s %s\n%s\n",
		styleLabel.Foreground(lipgloss.Color(nord13)).Render("● CPU"),
		barra(m.cpuPct, barW, cpuColor),
		lipgloss.NewStyle().Foreground(lipgloss.Color(cpuColor)).Bold(true).Render(fmt.Sprintf("%.1f%%", m.cpuPct)),
		styleDim.Render(fmt.Sprintf("   %d núcleos", m.cpuCores)),
	)
	ramColor := colorPct(m.ramPct, nord8)
	ramSection := fmt.Sprintf("%s\n%s %s\n%s\n",
		styleLabel.Foreground(lipgloss.Color(nord8)).Render("● RAM"),
		barra(m.ramPct, barW, ramColor),
		lipgloss.NewStyle().Foreground(lipgloss.Color(ramColor)).Bold(true).Render(fmt.Sprintf("%.1f%%", m.ramPct)),
		styleDim.Render(fmt.Sprintf("   %s / %s", fmtBytes(m.ramUsed), fmtBytes(m.ramTotal))),
	)
	diskColor := colorPct(m.diskPct, nord12)
	diskSection := fmt.Sprintf("%s\n%s %s\n%s\n",
		styleLabel.Foreground(lipgloss.Color(nord12)).Render("● DISCO"),
		barra(m.diskPct, barW, diskColor),
		lipgloss.NewStyle().Foreground(lipgloss.Color(diskColor)).Bold(true).Render(fmt.Sprintf("%.1f%%", m.diskPct)),
		styleDim.Render(fmt.Sprintf("   %.1f / %.1f GB", m.diskUsed, m.diskTotal)),
	)

	tempColor := nord14
	tempLabel := "normal"
	if m.temp >= 80 { tempColor = nord11; tempLabel = "¡alto!" } else if m.temp >= 65 { tempColor = nord12; tempLabel = "tibio" }
	var tempVal string
	if m.temp == 0 {
		tempVal = styleDim.Render("   N/D")
	} else {
		tempVal = lipgloss.NewStyle().Foreground(lipgloss.Color(tempColor)).Render(fmt.Sprintf("   %.0f°C — %s", m.temp, tempLabel))
	}
	tempSection := fmt.Sprintf("%s\n%s\n", styleLabel.Foreground(lipgloss.Color(nord11)).Render("● TEMPERATURA"), tempVal)

	netSection := fmt.Sprintf("%s\n%s\n%s\n",
		styleLabel.Foreground(lipgloss.Color(nord15)).Render("● RED"),
		lipgloss.NewStyle().Foreground(lipgloss.Color(nord14)).Render(fmt.Sprintf("   ▲ %.1f KB/s", m.netUp)),
		lipgloss.NewStyle().Foreground(lipgloss.Color(nord8)).Render(fmt.Sprintf("   ▼ %.1f KB/s", m.netDown)),
	)

	var statusLine string
	if m.statusMsg != "" {
		if m.statusIsErr {
			statusLine = "\n" + styleStatusErr.Render(m.statusMsg)
		} else {
			statusLine = "\n" + styleStatusOk.Render(m.statusMsg)
		}
	}

	leftContent := cpuSection + "\n" + ramSection + "\n" + diskSection + "\n" + tempSection + "\n" + netSection + statusLine
	leftPanel := styleBorder.Width(leftW).Height(panelH).Padding(0, 1).Render(leftContent)

	// RIGHT PANEL — scrollable process list
	visible := m.visibleRows()
	nameW := rightW - 34
	if nameW < 8 { nameW = 8 }
	total := len(m.procs)

	procTitle := styleLabel.Foreground(lipgloss.Color(nord9)).Render("● PROCESOS") +
		styleDim.Render(fmt.Sprintf(" (%d/%d)", m.cursor+1, total))
	procHeader := styleTableHeader.Render(fmt.Sprintf("  %-6s %-*s %6s %6s", "PID", nameW, "NOMBRE", "CPU%", "MEM%"))
	divider := styleDim.Render(strings.Repeat("─", rightW-4))

	lines := []string{procTitle, "", procHeader, divider}

	if m.scrollOffset > 0 {
		lines = append(lines, styleDim.Render(fmt.Sprintf("  ▲ %d más arriba", m.scrollOffset)))
	}

	end := m.scrollOffset + visible
	if end > total { end = total }

	for i := m.scrollOffset; i < end; i++ {
		p := m.procs[i]
		name := truncStr(p.Name, nameW)
		cpuC := nord14
		if p.CPU > 50 { cpuC = nord11 } else if p.CPU > 20 { cpuC = nord13 }

		if i == m.cursor {
			row := fmt.Sprintf("  %-6d %-*s %5.1f%% %5.1f%%", p.PID, nameW, name, p.CPU, p.Mem)
			lines = append(lines, styleSelected.Width(rightW-4).Render(row))
		} else {
			row := fmt.Sprintf("  %-6d %-*s %s %5.1f%%",
				p.PID, nameW, name,
				lipgloss.NewStyle().Foreground(lipgloss.Color(cpuC)).Render(fmt.Sprintf("%5.1f%%", p.CPU)),
				p.Mem,
			)
			if i%2 != 0 {
				row = lipgloss.NewStyle().Background(lipgloss.Color(nord1)).Render(row)
			}
			lines = append(lines, row)
		}
	}

	remaining := total - end
	if remaining > 0 {
		lines = append(lines, styleDim.Render(fmt.Sprintf("  ▼ %d más abajo", remaining)))
	}

	rightPanel := styleBorder.Width(rightW).Height(panelH).Padding(0, 1).Render(strings.Join(lines, "\n"))

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)

	// CONFIRM DIALOG
	var confirmOverlay string
	if m.confirm == confirmKill && m.cursor < len(m.procs) {
		sel := m.procs[m.cursor]
		yesStyle := styleKillNo
		noStyle := styleKillNo
		if m.confirmSel == 0 { yesStyle = styleKillYes } else { noStyle = styleKillYes }

		dialogContent := lipgloss.JoinVertical(lipgloss.Center,
			lipgloss.NewStyle().Foreground(lipgloss.Color(nord11)).Bold(true).Render("⚠  Matar proceso"),
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color(nord4)).Render(fmt.Sprintf("%s  (PID %d)", sel.Name, sel.PID)),
			"",
			lipgloss.JoinHorizontal(lipgloss.Center, yesStyle.Render("  Sí (Enter)  "), "   ", noStyle.Render("  No (Esc)  ")),
			"",
			styleDim.Render("← → para elegir"),
		)
		confirmOverlay = "\n\n" + styleConfirmBox.Render(dialogContent)
	}

	result := lipgloss.NewStyle().Padding(1, 2).Render(lipgloss.JoinVertical(lipgloss.Left, headerBox, panels))
	return result + confirmOverlay
}

func main() {
	m := Model{}
	m = recolectarDatos(m)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
