package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Yak036/mkd/internal/config"
)

var (
	setProvider     string
	setName         string
	setBaseURL      string
	setAPIKey       string
	setModel        string
	setSystem       string
	setClearKey     bool
	setAddModels    []string
	setRemoveModels []string
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Muestra o modifica la configuración multi-proveedor",
	Long: `Gestiona ~/.config/mkd/config.json.

Un proveedor comparte BaseURL y APIKey entre todos sus modelos. La forma más
cómoda de configurar MKD es abrir la TUI con "mkd" y usar Ctrl+K / Ctrl+M.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return showConfig()
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Muestra la configuración efectiva",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return showConfig()
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Imprime la ruta del archivo de configuración",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Crea o actualiza un proveedor",
	Example: `  mkd config set --provider deepinfra --api-key di-...
  mkd config set --provider openrouter --api-key sk-or-... --model anthropic/claude-3.5-sonnet
  mkd config set --provider ollama --base-url http://localhost:11434/v1 --model llama3.2
  mkd config set --provider deepinfra --add-model deepseek-ai/DeepSeek-V3
  mkd config set --provider openai --clear-key`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadFile()
		if err != nil {
			return err
		}

		key := strings.TrimSpace(setProvider)
		if key == "" {
			key = cfg.ActiveProvider
		}
		provider, ok := cfg.Providers[key]
		if !ok {
			provider = config.ProviderConfig{Name: config.DisplayName(key)}
		}

		changed := cmd.Flags().Changed("provider")
		if cmd.Flags().Changed("name") {
			provider.Name = strings.TrimSpace(setName)
			changed = true
		}
		if cmd.Flags().Changed("base-url") {
			baseURL := strings.TrimSpace(setBaseURL)
			if baseURL != "" {
				parsed, err := url.Parse(baseURL)
				if err != nil || parsed.Scheme == "" || parsed.Host == "" {
					return fmt.Errorf("base URL inválida: %q", baseURL)
				}
			}
			provider.BaseURL = baseURL
			changed = true
		}
		if cmd.Flags().Changed("api-key") {
			provider.APIKey = strings.TrimSpace(setAPIKey)
			changed = true
		}
		if cmd.Flags().Changed("model") {
			model := strings.TrimSpace(setModel)
			if model != "" {
				provider.Models = appendUnique(provider.Models, model)
				provider.ActiveModel = model
			}
			changed = true
		}
		for _, model := range setAddModels {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			provider.Models = appendUnique(provider.Models, model)
			if provider.ActiveModel == "" {
				provider.ActiveModel = model
			}
			changed = true
		}
		for _, model := range setRemoveModels {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			provider.Models = removeString(provider.Models, model)
			if provider.ActiveModel == model {
				provider.ActiveModel = ""
				if len(provider.Models) > 0 {
					provider.ActiveModel = provider.Models[0]
				}
			}
			changed = true
		}
		if cmd.Flags().Changed("system-prompt") {
			cfg.SystemPrompt = strings.TrimSpace(setSystem)
			changed = true
		}
		if setClearKey {
			provider.APIKey = ""
			changed = true
		}

		if !changed {
			return errors.New("indica al menos un flag: --provider, --name, --base-url, --api-key, --model, --add-model, --remove-model, --system-prompt o --clear-key")
		}
		if strings.TrimSpace(provider.Name) == "" {
			provider.Name = config.DisplayName(key)
		}

		cfg.Providers[key] = provider
		cfg.ActiveProvider = key
		cfg.Normalize()

		path, err := config.Save(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Proveedor %q guardado en %s\n", key, path)
		return nil
	},
}

var configUseCmd = &cobra.Command{
	Use:   "use <proveedor>",
	Short: "Cambia el proveedor activo",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := strings.TrimSpace(args[0])
		cfg, err := config.LoadFile()
		if err != nil {
			return err
		}
		if _, ok := cfg.Providers[key]; !ok {
			return fmt.Errorf("proveedor %q no existe; disponibles: %s", key, strings.Join(cfg.ProviderKeys(), ", "))
		}
		cfg.ActiveProvider = key
		cfg.Normalize()
		path, err := config.Save(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Proveedor activo: %s (%s)\n", cfg.Provider(), path)
		return nil
	},
}

func init() {
	configSetCmd.Flags().StringVar(&setProvider, "provider", "", "clave del proveedor (deepinfra, openrouter, openai, ollama…)")
	configSetCmd.Flags().StringVar(&setName, "name", "", "nombre visible del proveedor")
	configSetCmd.Flags().StringVar(&setBaseURL, "base-url", "", "URL base compatible con OpenAI")
	configSetCmd.Flags().StringVar(&setAPIKey, "api-key", "", "API key del proveedor")
	configSetCmd.Flags().StringVarP(&setModel, "model", "m", "", "modelo activo (se agrega a la lista)")
	configSetCmd.Flags().StringArrayVar(&setAddModels, "add-model", nil, "agrega un modelo a la lista; se puede repetir")
	configSetCmd.Flags().StringArrayVar(&setRemoveModels, "remove-model", nil, "elimina un modelo de la lista; se puede repetir")
	configSetCmd.Flags().StringVar(&setSystem, "system-prompt", "", "system prompt global")
	configSetCmd.Flags().BoolVar(&setClearKey, "clear-key", false, "elimina la API key guardada")

	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUseCmd)
}

func showConfig() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}

	state := "ningún modelo disponible actualmente"
	if cfg.Configured() {
		state = "listo"
	}

	fmt.Printf("Archivo:    %s\n", path)
	fmt.Printf("Estado:     %s\n", state)
	fmt.Printf("Activo:     %s (%s)\n", cfg.Provider(), cfg.ActiveProvider)
	fmt.Printf("System:     %s\n", valueOr(cfg.SystemPrompt, "(sin system prompt)"))
	fmt.Println()

	for _, key := range cfg.ProviderKeys() {
		provider := cfg.Providers[key]
		marker := " "
		if key == cfg.ActiveProvider {
			marker = "*"
		}
		fmt.Printf("%s [%s] %s\n", marker, key, provider.Name)
		fmt.Printf("    Base URL: %s\n", valueOr(provider.BaseURL, "(sin configurar)"))
		fmt.Printf("    API key:  %s\n", config.MaskKey(provider.APIKey))
		fmt.Printf("    Modelo:   %s\n", valueOr(provider.ActiveModel, "(sin modelo)"))
		fmt.Printf("    Modelos:  %d · %s\n", len(provider.Models), strings.Join(provider.Models, ", "))
		fmt.Println()
	}
	return nil
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func appendUnique(values []string, target string) []string {
	for _, value := range values {
		if value == target {
			return values
		}
	}
	return append(values, target)
}

func removeString(values []string, target string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
