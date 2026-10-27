package storage

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// ModelsCacheVersion permite invalidar cachés de esquemas anteriores.
const ModelsCacheVersion = 1

// ModelsCache guarda los modelos conocidos por proveedor para que Ctrl+M
// cargue al instante. Solo se reescribe cuando el usuario pulsa Ctrl+F.
type ModelsCache struct {
	Version   int                 `json:"version"`
	UpdatedAt time.Time           `json:"updated_at"`
	Providers map[string][]string `json:"providers"`
}

// LoadModelsCache lee el caché. Si no existe devuelve uno vacío sin error.
func LoadModelsCache() (ModelsCache, error) {
	empty := ModelsCache{Version: ModelsCacheVersion, Providers: map[string][]string{}}

	path, err := modelsCachePath()
	if err != nil {
		return empty, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}

	var cache ModelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return empty, err
	}
	if cache.Version != ModelsCacheVersion {
		return empty, nil
	}
	if cache.Providers == nil {
		cache.Providers = map[string][]string{}
	}
	return cache, nil
}

// Models devuelve los modelos cacheados de un proveedor.
func (c ModelsCache) Models(provider string) []string {
	models := c.Providers[provider]
	return append([]string(nil), models...)
}

// SaveModelsCache reemplaza los modelos de un proveedor y persiste el archivo.
func SaveModelsCache(cache ModelsCache, provider string, models []string) (ModelsCache, error) {
	if cache.Providers == nil {
		cache.Providers = map[string][]string{}
	}
	cache.Version = ModelsCacheVersion
	cache.UpdatedAt = time.Now()
	cache.Providers[provider] = append([]string(nil), models...)

	path, err := modelsCachePath()
	if err != nil {
		return cache, err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return cache, err
	}
	data = append(data, '\n')
	return cache, writeAtomic(path, data)
}
