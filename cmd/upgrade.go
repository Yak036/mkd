package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Yak036/mkd/internal/update"
	"github.com/Yak036/mkd/internal/version"
)

// upgradeCmd comprueba e instala la última versión publicada.
//
//	mkd upgrade            # actualiza si hay versión nueva
//	mkd upgrade --check    # solo informa, sin descargar
var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Actualiza mkd a la última versión publicada",
	Long: `Descarga la última release de GitHub, verifica su checksum SHA-256 y
reemplaza el binario en ejecución.

En Windows no se puede sobrescribir un ejecutable en uso: se conserva el
anterior como mkd.exe.old y se borra automáticamente en el siguiente arranque.`,
	Example: `  mkd upgrade
  mkd upgrade --check`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		checkOnly, _ := cmd.Flags().GetBool("check")
		info := version.Get()
		out := cmd.OutOrStdout()

		if checkOnly {
			check := update.Fetch(cmd.Context(), info.Version)
			if check.Err != "" {
				return fmt.Errorf("no se pudo comprobar la versión: %s", check.Err)
			}
			if !check.Available {
				fmt.Fprintln(out, "Estás en la última versión:", strings.TrimPrefix(info.Version, "v"))
				return nil
			}
			fmt.Fprintf(out, "Nueva versión disponible: %s (tienes %s)\n",
				check.Latest, strings.TrimPrefix(info.Version, "v"))
			if check.URL != "" {
				fmt.Fprintln(out, check.URL)
			}
			fmt.Fprintln(out, "Ejecuta `mkd upgrade` para actualizar.")
			return nil
		}

		fmt.Fprintln(out, "Comprobando actualizaciones…")
		installed, err := update.Upgrade(cmd.Context(), info.Version)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ mkd actualizado a %s\n", strings.TrimPrefix(installed, "v"))
		return nil
	},
}

func init() {
	upgradeCmd.Flags().Bool("check", false, "solo comprobar si hay versión nueva, sin descargar")
}
