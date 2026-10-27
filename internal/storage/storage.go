// Package storage persiste sesiones y cachés locales de MKD en
// $XDG_DATA_HOME/mkd (por defecto ~/.local/share/mkd).
//
//	~/.local/share/mkd/
//	├── sessions/
//	│   └── <session_id>.json
//	└── models_cache.json
package storage

import (
	"os"
	"path/filepath"
)

// AppName es el subdirectorio dentro del directorio de datos del usuario.
const AppName = "mkd"

// BaseDir devuelve ~/.local/share/mkd, creándolo con permisos 0700.
func BaseDir() (string, error) {
	base, err := os.UserHomeDir()
	if err == nil && base != "" {
		dir := filepath.Join(base, ".local", "share", AppName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return dir, nil
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, AppName, "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// sessionsDir devuelve (y crea) el directorio de sesiones.
func sessionsDir() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// modelsCachePath devuelve la ruta del caché de modelos.
func modelsCachePath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "models_cache.json"), nil
}

// writeAtomic escribe data en path usando un temporal + rename.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
