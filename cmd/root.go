// Package cmd define los comandos de MKD.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/glamour"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/update"
	"github.com/Yak036/mkd/internal/version"
)

const (
	defaultPrompt  = "Analiza el contenido adjunto y responde de forma concisa."
	maxStdinBytes  = 8 << 20
	maxFileBytes   = 1 << 20
	fallbackWidth  = 100
	appDescription = "MKD-CLI: asistente de IA para terminal con modo pipe, one-shot y TUI."
)

var (
	flagProvider string
	flagModel    string
	flagFiles    []string
	flagSystem   string
)

var rootCmd = &cobra.Command{
	Use:   "mkd [prompt...]",
	Short: "Asistente de IA para terminal (pipe, one-shot y TUI)",
	Long: appDescription + `

Sin argumentos y en una terminal interactiva abre la TUI. Si hay argumentos
o datos por stdin, responde en modo one-shot y termina.`,
	Example: `  mkd "¿cómo uso awk?"
  cat main.go | mkd "optimiza esto"
  mkd -f main.go -f go.mod "revisa la arquitectura"
  mkd -s "Responde solo con código" "un servidor HTTP en Go"
  mkd chat`,
	Version:       version.Get().Version,
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		stdin, err := readStdin()
		if err != nil {
			return err
		}

		prompt := strings.TrimSpace(strings.Join(args, " "))
		if prompt == "" && stdin == "" {
			if isTTY(os.Stdin) && isTTY(os.Stdout) {
				return runChat(cmd)
			}
			return errors.New(`falta el prompt (ejemplo: mkd "tu pregunta")`)
		}

		return runOneShot(cmd.Context(), prompt, stdin)
	},
}

var askCmd = &cobra.Command{
	Use:   "ask [prompt...]",
	Short: "Responde una sola vez y termina",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		stdin, err := readStdin()
		if err != nil {
			return err
		}

		prompt := strings.TrimSpace(strings.Join(args, " "))
		if prompt == "" && stdin == "" {
			return errors.New(`falta el prompt (ejemplo: mkd ask "tu pregunta")`)
		}

		return runOneShot(cmd.Context(), prompt, stdin)
	},
}

// Execute es el punto de entrada llamado desde main.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rootCmd.SetContext(ctx)

	// Windows deja el binario anterior como .old tras un `mkd upgrade`.
	update.CleanupOldBinary()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "mkd:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagProvider, "provider", "p", "", "proveedor a usar (clave de la configuración)")
	rootCmd.PersistentFlags().StringVarP(&flagModel, "model", "m", "", "modelo a usar (sobreescribe la configuración)")
	rootCmd.PersistentFlags().StringArrayVarP(&flagFiles, "file", "f", nil, "archivo de contexto; se puede repetir")
	rootCmd.PersistentFlags().StringVarP(&flagSystem, "system", "s", "", "system prompt (sobreescribe la configuración)")

	rootCmd.AddCommand(askCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(upgradeCmd)
}

// effectiveConfig combina archivo, entorno y flags.
func effectiveConfig() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, err
	}

	if provider := strings.TrimSpace(flagProvider); provider != "" {
		if _, ok := cfg.Providers[provider]; !ok {
			cfg.Providers[provider] = config.ProviderConfig{Name: config.DisplayName(provider)}
		}
		cfg.ActiveProvider = provider
	}

	active, ok := cfg.Active()
	if !ok {
		active = config.ProviderConfig{Name: config.DisplayName(cfg.ActiveProvider)}
	}
	if model := strings.TrimSpace(flagModel); model != "" {
		active.ActiveModel = model
		cfg.Providers[cfg.ActiveProvider] = active
	}
	if system := strings.TrimSpace(flagSystem); system != "" {
		cfg.SystemPrompt = system
	}

	cfg.Normalize()
	return cfg, nil
}

func runOneShot(ctx context.Context, prompt, stdin string) error {
	cfg, err := effectiveConfig()
	if err != nil {
		return err
	}

	client, err := ai.New(cfg)
	if err != nil {
		return err
	}

	if prompt == "" {
		prompt = defaultPrompt
	}
	userMessage, err := buildUserMessage(prompt, stdin, flagFiles)
	if err != nil {
		return err
	}

	chunks, err := client.Stream(ctx, cfg.SystemPrompt, []ai.Message{
		{Role: ai.RoleUser, Content: userMessage},
	})
	if err != nil {
		return err
	}

	// En una terminal se renderiza Markdown al final. En una tubería se
	// escribe texto plano token a token para poder encadenar procesos.
	if !isTTY(os.Stdout) {
		for chunk := range chunks {
			if chunk.Err != nil {
				return chunk.Err
			}
			if _, err := fmt.Fprint(os.Stdout, chunk.Content); err != nil {
				return fmt.Errorf("stdout: %w", err)
			}
		}
		return nil
	}

	clearProgress := func() {}
	if isTTY(os.Stderr) {
		fmt.Fprint(os.Stderr, "MKD generando…")
		clearProgress = func() { fmt.Fprint(os.Stderr, "\r\033[K") }
	}
	defer clearProgress()

	var response strings.Builder
	for chunk := range chunks {
		if chunk.Err != nil {
			return chunk.Err
		}
		response.WriteString(chunk.Content)
	}
	clearProgress()

	fmt.Print(renderOutput(response.String(), terminalWidth()))
	return nil
}

// buildUserMessage adjunta stdin y archivos en bloques XML antes del prompt.
func buildUserMessage(prompt, stdin string, files []string) (string, error) {
	var b strings.Builder

	if stdin != "" {
		b.WriteString("<stdin>\n")
		b.WriteString(stdin)
		if !strings.HasSuffix(stdin, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("</stdin>\n\n")
	}

	for _, path := range files {
		content, err := readContextFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "<file path=%q>\n", path)
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("</file>\n\n")
	}

	b.WriteString(prompt)
	return b.String(), nil
}

func readContextFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("archivo %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("archivo %s: es un directorio", path)
	}
	if info.Size() > maxFileBytes {
		return "", fmt.Errorf("archivo %s: supera el límite de 1 MiB", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("archivo %s: %w", path, err)
	}
	return string(data), nil
}

// readStdin devuelve el contenido de stdin solo si es una tubería o archivo.
func readStdin() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}

	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes+1))
	if err != nil {
		return "", fmt.Errorf("leer stdin: %w", err)
	}
	if len(data) > maxStdinBytes {
		return "", fmt.Errorf("stdin supera el límite de %d MiB", maxStdinBytes>>20)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func renderOutput(text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(max(width-2, 20)),
	)
	if err != nil {
		return text + "\n"
	}

	out, err := renderer.Render(text)
	if err != nil {
		return text + "\n"
	}
	return strings.Trim(out, "\n") + "\n"
}

func terminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return fallbackWidth
	}
	return width
}

func isTTY(file *os.File) bool {
	return term.IsTerminal(int(file.Fd()))
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Muestra versión, commit, fecha de build y plataforma",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), version.Get().Debug())
		return nil
	},
}
