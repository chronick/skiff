package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"fyne.io/systray"

	"github.com/chronick/plane/internal/client"
)

const maxResourceItems = 20

var (
	cl            *client.Client
	resourceItems []*systray.MenuItem
	mStatus       *systray.MenuItem
)

func main() {
	socketPath := client.DefaultSocketPath()
	if sp := os.Getenv("PLANE_SOCKET"); sp != "" {
		socketPath = sp
	}
	cl = client.New(socketPath)
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetTitle("✈️")
	systray.SetTooltip("plane - container orchestration")

	mStatus = systray.AddMenuItem("plane: connecting...", "Daemon status")
	mStatus.Disable()

	systray.AddSeparator()

	// Pre-allocate resource items (hidden by default)
	for i := 0; i < maxResourceItems; i++ {
		item := systray.AddMenuItem("", "")
		item.Disable()
		item.Hide()
		resourceItems = append(resourceItems, item)
	}

	systray.AddSeparator()

	mStartAll := systray.AddMenuItem("▶  Start All", "Start all resources")
	mStopAll := systray.AddMenuItem("■  Stop All", "Stop all resources")

	systray.AddSeparator()

	mOpenTUI := systray.AddMenuItem("Open TUI...", "Open terminal interface")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit plane menu")

	go func() {
		for {
			select {
			case <-mStartAll.ClickedCh:
				go cl.Up(nil)
			case <-mStopAll.ClickedCh:
				go cl.Down(nil)
			case <-mOpenTUI.ClickedCh:
				go openTUI()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()

	go refreshLoop()
}

func refreshLoop() {
	for {
		snap, err := cl.Status()
		if err != nil {
			mStatus.SetTitle("plane: disconnected")
			systray.SetTitle("✈️")
			systray.SetTooltip("plane - disconnected")
			for _, item := range resourceItems {
				item.Hide()
			}
			time.Sleep(5 * time.Second)
			continue
		}

		running := 0
		total := len(snap.Resources)
		for _, r := range snap.Resources {
			if r.State == "running" {
				running++
			}
		}

		if total > 0 {
			systray.SetTitle(fmt.Sprintf("✈️ %d/%d", running, total))
			mStatus.SetTitle(fmt.Sprintf("plane: %d/%d running", running, total))
		} else {
			systray.SetTitle("✈️")
			mStatus.SetTitle("plane: no resources")
		}
		systray.SetTooltip(fmt.Sprintf("plane - %d/%d resources running", running, total))

		// Update resource items
		for i := 0; i < maxResourceItems; i++ {
			if i < len(snap.Resources) {
				r := snap.Resources[i]
				icon := stateIcon(r.State)
				resourceItems[i].SetTitle(fmt.Sprintf("%s  %s — %s", icon, r.Name, r.State))
				resourceItems[i].Show()
			} else {
				resourceItems[i].Hide()
			}
		}

		// Update schedule items (shown after resources)
		// Schedules are listed after resources in the same pool
		offset := len(snap.Resources)
		for i, s := range snap.Schedules {
			idx := offset + i
			if idx >= maxResourceItems {
				break
			}
			icon := scheduleIcon(s.LastResult)
			resourceItems[idx].SetTitle(fmt.Sprintf("%s  %s — %s", icon, s.Name, s.LastResult))
			resourceItems[idx].Show()
		}

		time.Sleep(5 * time.Second)
	}
}

func stateIcon(state string) string {
	switch state {
	case "running":
		return "🟢"
	case "failed":
		return "🔴"
	case "starting":
		return "🟡"
	default:
		return "⚪"
	}
}

func scheduleIcon(result string) string {
	switch result {
	case "success":
		return "🟢"
	case "failed":
		return "🔴"
	case "running":
		return "🟡"
	default:
		return "⏱"
	}
}

func openTUI() {
	// Find the plane binary
	planePath, err := exec.LookPath("plane")
	if err != nil {
		// Fallback: try same directory as this binary
		if exePath, err := os.Executable(); err == nil {
			planePath = exePath
		}
	}

	script := fmt.Sprintf(`tell application "Terminal"
	do script "%s tui"
	activate
end tell`, planePath)
	exec.Command("osascript", "-e", script).Run()
}

func onExit() {}
