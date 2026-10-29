// Package antigravity implementa la integración nativa con el motor y cuenta de Google Antigravity (CLI agy).
package antigravity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionInfo describe el estado de autenticación y disponibilidad de Antigravity en la máquina.
type SessionInfo struct {
	Account     string `json:"account"`      // Correo de Google activo (ej. ramcesvedes@gmail.com)
	AgyPath     string `json:"agy_path"`     // Ruta absoluta al ejecutable agy
	IsAvailable bool   `json:"is_available"` // Indica si el binario agy está disponible y operable
}

// googleAccountsFile estructura el archivo ~/.gemini/google_accounts.json.
type googleAccountsFile struct {
	Active string   `json:"active"`
	Old    []string `json:"old"`
}

// FindAgyBinary busca el ejecutable agy en las rutas estándar del usuario y en el PATH del sistema.
func FindAgyBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("AGY_BIN")); custom != "" {
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			return custom, nil
		}
	}
	if custom := strings.TrimSpace(os.Getenv("ANTIGRAVITY_BIN")); custom != "" {
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			return custom, nil
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		stdPath := filepath.Join(home, ".local", "bin", "agy")
		if info, err := os.Stat(stdPath); err == nil && !info.IsDir() {
			return stdPath, nil
		}
	}

	path, err := exec.LookPath("agy")
	if err == nil {
		return path, nil
	}

	return "", errors.New("no se encontró el ejecutable 'agy' de Antigravity en el sistema")
}

// LoadLocalAuth detecta la cuenta activa de Google Antigravity y la ubicación del binario.
func LoadLocalAuth() (SessionInfo, error) {
	agyPath, err := FindAgyBinary()
	if err != nil {
		return SessionInfo{}, err
	}

	account := ""
	home, err := os.UserHomeDir()
	if err == nil {
		accPath := filepath.Join(home, ".gemini", "google_accounts.json")
		if data, err := os.ReadFile(accPath); err == nil {
			var af googleAccountsFile
			if err := json.Unmarshal(data, &af); err == nil && af.Active != "" {
				account = strings.TrimSpace(af.Active)
			}
		}
	}

	if account == "" {
		account = "cuenta local de Antigravity"
	}

	return SessionInfo{
		Account:     account,
		AgyPath:     agyPath,
		IsAvailable: true,
	}, nil
}

// EnsureAgyReady valida que agy esté instalado y devuelve su ruta absoluta.
func EnsureAgyReady() (string, error) {
	info, err := LoadLocalAuth()
	if err != nil {
		return "", fmt.Errorf("Antigravity no disponible: %w", err)
	}
	if !info.IsAvailable || info.AgyPath == "" {
		return "", errors.New("la CLI 'agy' no está lista para su ejecución")
	}
	return info.AgyPath, nil
}
