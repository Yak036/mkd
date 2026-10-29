// Package cmd define los comandos de MKD.
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/codex"
	"github.com/Yak036/mkd/internal/config"
)

var (
	flagImportLocal bool
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Gestiona la autenticación con proveedores y suscripciones",
	Long: `Permite iniciar sesión con tu cuenta de OpenAI Codex (ChatGPT Plus / Pro / Team)
usando OAuth 2.0 PKCE en el navegador o importando tu sesión existente de ~/.codex/auth.json.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var authCodexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Inicia sesión con tu suscripción de OpenAI Codex / ChatGPT",
	Example: `  # Flujo oficial en navegador (OAuth 2.0 PKCE):
  mkd auth codex

  # Importar sesión existente de ~/.codex/auth.json:
  mkd auth codex --import`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadFile()
		if err != nil {
			return err
		}

		var bundle codex.TokenBundle
		if flagImportLocal {
			fmt.Println("Buscando sesión existente en ~/.codex/auth.json…")
			bundle, err = codex.LoadLocalAuth()
			if err != nil {
				return fmt.Errorf("no se pudo importar la sesión local: %w", err)
			}
			fmt.Printf("✓ Sesión local encontrada (Account: %s)\n", defaultIfEmpty(bundle.AccountID, "estándar"))
		} else {
			fmt.Println("Iniciando flujo de autenticación con OpenAI Codex (OAuth PKCE)…")
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()

			bundle, err = codex.LoginPKCE(ctx, func(authURL string) {
				fmt.Println("\nAbre el siguiente enlace en tu navegador para autorizar MKD:")
				fmt.Println("\n  " + authURL + "\n")
				fmt.Println("Esperando confirmación en el navegador (puerto local 1455)…")
			})
			if err != nil {
				return fmt.Errorf("error durante la autenticación: %w", err)
			}
			fmt.Println("\n✓ Autorización completada con éxito.")
		}

		// Guardar copia local en ~/.codex/auth.json para compatibilidad
		_ = codex.SaveLocalAuth(bundle)

		// Actualizar proveedor "codex" en config.json
		p, ok := cfg.Providers["codex"]
		if !ok {
			p = config.ProviderConfig{
				Name:        "Codex (ChatGPT)",
				BaseURL:     codex.DefaultBaseURL,
				ActiveModel: "gpt-5.3-codex",
			}
		}
		p.APIKey = bundle.AccessToken
		p.RefreshToken = bundle.RefreshToken
		p.AccountID = bundle.AccountID
		p.TokenExpires = bundle.ExpiresAt
		cfg.Providers["codex"] = p
		cfg.ActiveProvider = "codex"

		// Sincronizar modelos disponibles para esta suscripción
		fmt.Print("Sincronizando modelos de tu plan… ")
		client, err := ai.New(cfg)
		if err == nil {
			if models, err := client.FetchAvailableModels(cmd.Context()); err == nil && len(models) > 0 {
				p.Models = models
				if !containsString(p.Models, p.ActiveModel) {
					p.ActiveModel = p.Models[0]
				}
				cfg.Providers["codex"] = p
				fmt.Printf("%d modelos encontrados.\n", len(models))
			} else {
				fmt.Println("usando modelos base por defecto.")
			}
		} else {
			fmt.Println("completado.")
		}

		path, err := config.Save(cfg)
		if err != nil {
			return fmt.Errorf("no se pudo guardar la configuración: %w", err)
		}

		fmt.Printf("\n✓ Proveedor 'codex' activado y configurado en %s\n", path)
		fmt.Printf("  Modelo activo: %s\n", p.ActiveModel)
		fmt.Println("\nPuedes iniciar la TUI ejecutando: mkd chat")
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Muestra el estado de autenticación y suscripción",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadFile()
		if err != nil {
			return err
		}

		fmt.Printf("Proveedor activo: %s\n", cfg.ActiveProvider)

		// Estado de Codex
		p, hasCodex := cfg.Providers["codex"]
		fmt.Println("\n[Codex (ChatGPT)]")
		if hasCodex && (p.APIKey != "" || p.RefreshToken != "") {
			fmt.Println("  Estado: Sesión configurada en MKD")
			if p.AccountID != "" {
				fmt.Printf("  Account ID: %s\n", p.AccountID)
			}
			if p.TokenExpires > 0 {
				exp := time.Unix(p.TokenExpires, 0)
				if time.Now().Before(exp) {
					fmt.Printf("  Expira: %s (en %s)\n", exp.Format("2006-01-02 15:04:05"), time.Until(exp).Round(time.Minute))
				} else {
					fmt.Println("  Token expirado (se refrescará automáticamente con el refresh token)")
				}
			}
			fmt.Printf("  Modelo activo: %s\n", p.ActiveModel)
			if len(p.Models) > 0 {
				fmt.Printf("  Modelos: %s\n", strings.Join(p.Models, ", "))
			}
		} else {
			fmt.Println("  Estado: No configurado en config.json")
		}

		// Chequear si existe sesión en ~/.codex/auth.json
		if local, err := codex.LoadLocalAuth(); err == nil {
			fmt.Println("\n[Sesión externa detectada en ~/.codex/auth.json]")
			fmt.Printf("  Account: %s\n", defaultIfEmpty(local.AccountID, "estándar"))
			if local.ExpiresAt > 0 {
				fmt.Printf("  Expira: %s\n", time.Unix(local.ExpiresAt, 0).Format("2006-01-02 15:04:05"))
			}
			fmt.Println("  (Puedes importarla a MKD con: mkd auth codex --import)")
		}

		return nil
	},
}

func init() {
	authCodexCmd.Flags().BoolVar(&flagImportLocal, "import", false, "importa la sesión existente de ~/.codex/auth.json en lugar de abrir el navegador")
	authCmd.AddCommand(authCodexCmd)
	authCmd.AddCommand(authStatusCmd)
}

func defaultIfEmpty(val, fallback string) string {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	return val
}

func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
