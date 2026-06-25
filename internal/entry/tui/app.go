package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/logger"
)

// Run starts the TUI.
// Startup mode layering convention:
// 1. Quick mode and co-creation mode belong to "startup orchestration";
// 2. Official writing sessions enter host.Host;
// 3. If new shared modes like "continue writing existing novel" are added in the future, they should go under internal/entry/startup.
func Run(cfg bootstrap.Config, bundle assets.Bundle, version string) error {
	rt, err := host.New(cfg, bundle)
	if err != nil {
		return err
	}
	bridge := newAskUserBridge()
	rt.AskUser().SetHandler(bridge.handler)
	cleanup := logger.SetupFile(rt.Dir(), "tui.log", false)
	defer cleanup()
	defer rt.Close()

	m := NewModel(rt, bridge, version)
	// Do not enable mouse reporting globally at startup: the welcome page does not need mouse input, and disabling it preserves the terminal's native
	// drag-to-select copy behavior. Mouse reporting will be enabled by enterRunning when entering the writing workspace (modeRunning),
	// to support clicking to switch panels / scroll wheel / dragging the sidebar.
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
