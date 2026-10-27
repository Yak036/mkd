package cmd

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/mcp"
	"github.com/Yak036/mkd/internal/ui"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Abre el workspace TUI",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChat(cmd)
	},
}

func runChat(_ *cobra.Command) error {
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		return errors.New("`mkd chat` requiere una terminal interactiva")
	}

	cfg, err := effectiveConfig()
	if err != nil {
		return err
	}

	client, err := ai.New(cfg)
	if err != nil {
		return err
	}

	// Gestor MCP: los servidores habilitados arrancan en el Init del workspace
	// y se cierran al salir (quit o Ctrl+C).
	manager := mcp.NewManager(cfg.MCPServers)

	model := ui.New(ui.Options{
		Client:       client,
		Config:       cfg,
		Files:        flagFiles,
		MCP:          manager,
		UpdateNotice: passiveUpdateNotice(),
	})

	program := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := program.Run(); err != nil {
		manager.Close()
		return fmt.Errorf("TUI: %w", err)
	}
	manager.Close()
	return nil
}
