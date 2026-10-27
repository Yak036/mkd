// Package swarm implementa el motor de orquestación jerárquica multi-agente de
// MKD: perfiles de sub-agente, bus de eventos concurrente y orquestador con
// herramientas de delegación en serie y en paralelo.
//
//	┌────────────────────────────────┐
//	│  Orquestador (agente principal) │
//	└───────────────┬────────────────┘
//	delegate_task   │   delegate_parallel
//	      ┌─────────┴─────────┐
//	      ▼                   ▼
//	┌───────────┐       ┌───────────┐  goroutines
//	│ explorer  │       │ coder     │  + WaitGroup
//	└─────┬─────┘       └─────┬─────┘
//	      └──── SwarmEvent ────┘
//	               (chan) ──► Bubbletea
//
// profiles.go define los perfiles por defecto, búsqueda y helpers de CRUD sobre
// config.SubAgentProfile. La normalización canónica vive en config (para evitar
// un ciclo de importación) y aquí se reexporta.
package swarm

import (
	"fmt"
	"sort"
	"strings"

	"mkd/internal/config"
)

// IDs de los perfiles preinstalados.
const (
	ExplorerID = config.SubAgentExplorer
	CoderID    = config.SubAgentCoder
	QAID       = config.SubAgentQA
	DocsID     = config.SubAgentDocs
)

// ToolAll indica que el sub-agente puede usar todas las tools nativas.
const ToolAll = "*"

// MCPWildcard habilita todas las tools MCP en AllowedTools.
const MCPWildcard = "mcp:*"

// DefaultProfiles devuelve los cuatro sub-agentes base listos para usar.
func DefaultProfiles() []config.SubAgentProfile {
	agents := config.DefaultSubAgents()
	profiles := make([]config.SubAgentProfile, 0, len(agents))
	for _, profile := range agents {
		profiles = append(profiles, profile)
	}
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles
}

// DefaultMap devuelve los perfiles por defecto indexados por ID.
func DefaultMap() map[string]config.SubAgentProfile {
	return config.DefaultSubAgents()
}

// Normalize limpia un mapa de perfiles aplicando las reglas canónicas.
func Normalize(agents map[string]config.SubAgentProfile) map[string]config.SubAgentProfile {
	return config.NormalizeSubAgents(agents)
}

// NormalizeColor valida #RRGGBB (delegado a config).
func NormalizeColor(value string) string {
	return config.NormalizeAgentColor(value)
}

// SortProfiles devuelve los perfiles ordenados por nombre para la TUI.
func SortProfiles(agents map[string]config.SubAgentProfile) []config.SubAgentProfile {
	profiles := make([]config.SubAgentProfile, 0, len(agents))
	for _, profile := range agents {
		profiles = append(profiles, profile)
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
	return profiles
}

// EnabledProfiles devuelve los perfiles activos (orden estable por ID).
func EnabledProfiles(agents map[string]config.SubAgentProfile) []config.SubAgentProfile {
	profiles := make([]config.SubAgentProfile, 0, len(agents))
	for _, profile := range agents {
		if profile.Enabled {
			profiles = append(profiles, profile)
		}
	}
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles
}

// FindProfile busca un perfil por ID (case-insensitive).
func FindProfile(agents map[string]config.SubAgentProfile, id string) (config.SubAgentProfile, bool) {
	id = strings.TrimSpace(id)
	if profile, ok := agents[id]; ok {
		return profile, true
	}
	lower := strings.ToLower(id)
	for key, profile := range agents {
		if strings.ToLower(key) == lower || strings.ToLower(profile.ID) == lower {
			return profile, true
		}
	}
	return config.SubAgentProfile{}, false
}

// UpsertProfile inserta o reemplaza un perfil validado.
func UpsertProfile(agents map[string]config.SubAgentProfile, profile config.SubAgentProfile) (map[string]config.SubAgentProfile, error) {
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		return agents, fmt.Errorf("el sub-agente necesita un ID")
	}
	if strings.ContainsAny(profile.ID, " \t\n/\\") {
		return agents, fmt.Errorf("el ID no puede contener espacios ni separadores: %q", profile.ID)
	}
	if agents == nil {
		agents = map[string]config.SubAgentProfile{}
	}
	normalized := config.NormalizeSubAgents(map[string]config.SubAgentProfile{profile.ID: profile})
	agents[profile.ID] = normalized[profile.ID]
	return agents, nil
}

// DeleteProfile elimina un perfil del mapa. Devuelve el mapa y si borró algo.
func DeleteProfile(agents map[string]config.SubAgentProfile, id string) (map[string]config.SubAgentProfile, bool) {
	profile, ok := FindProfile(agents, id)
	if !ok {
		return agents, false
	}
	delete(agents, profile.ID)
	return agents, true
}

// ToggleProfile invierte el flag Enabled de un perfil.
func ToggleProfile(agents map[string]config.SubAgentProfile, id string) (map[string]config.SubAgentProfile, bool) {
	profile, ok := FindProfile(agents, id)
	if !ok {
		return agents, false
	}
	profile.Enabled = !profile.Enabled
	agents[profile.ID] = profile
	return agents, profile.Enabled
}

// ToolAllowed indica si un perfil puede invocar una tool concreta.
//
//	"*"         → cualquier tool nativa (no MCP)
//	"mcp:*"     → cualquier tool MCP (prefijo mcp__)
//	"read_file" → esa tool exacta
func ToolAllowed(profile config.SubAgentProfile, tool string) bool {
	tools := profile.AllowedTools
	if len(tools) == 0 {
		return true
	}
	isMCP := strings.HasPrefix(tool, "mcp__")
	for _, allowed := range tools {
		allowed = strings.TrimSpace(allowed)
		switch {
		case allowed == "" || allowed == ToolAll:
			if !isMCP {
				return true
			}
		case allowed == MCPWildcard:
			if isMCP {
				return true
			}
		case allowed == tool:
			return true
		}
	}
	return false
}

// ToolsForPermissions devuelve la lista legible de tools de un perfil.
func ToolsForPermissions(profile config.SubAgentProfile) string {
	tools := profile.AllowedTools
	if len(tools) == 0 {
		return "todas"
	}
	labels := make([]string, 0, len(tools))
	for _, tool := range tools {
		switch tool {
		case ToolAll:
			labels = append(labels, "todas")
		case MCPWildcard:
			labels = append(labels, "mcp:*")
		case "read_file":
			labels = append(labels, "read")
		case "list_dir":
			labels = append(labels, "list")
		case "search_code":
			labels = append(labels, "search")
		case "write_file":
			labels = append(labels, "write")
		case "patch_file":
			labels = append(labels, "patch")
		case "run_command":
			labels = append(labels, "run")
		default:
			labels = append(labels, tool)
		}
	}
	if len(labels) == 0 {
		return "todas"
	}
	return strings.Join(labels, ", ")
}

// CloneProfiles devuelve una copia profunda del mapa de perfiles.
func CloneProfiles(agents map[string]config.SubAgentProfile) map[string]config.SubAgentProfile {
	out := make(map[string]config.SubAgentProfile, len(agents))
	for key, profile := range agents {
		profile.AllowedTools = append([]string(nil), profile.AllowedTools...)
		out[key] = profile
	}
	return out
}
