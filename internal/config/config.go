// Package config gestiona la configuración multi-proveedor de MKD.
//
// Esquema:
//
//	{
//	  "active_provider": "deepinfra",
//	  "system_prompt": "...",
//	  "providers": {
//	    "deepinfra": {
//	      "name": "DeepInfra",
//	      "base_url": "https://api.deepinfra.com/v1/openai",
//	      "api_key": "...",
//	      "active_model": "Qwen/Qwen2.5-Coder-32B-Instruct",
//	      "models": ["..."]
//	    }
//	  }
//	}
//
// Un proveedor comparte BaseURL y APIKey entre todos sus modelos. El archivo
// vive en $XDG_CONFIG_HOME/mkd/config.json y se escribe con permisos 0600.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// AppName es el directorio dentro del directorio de configuración.
	AppName = "mkd"
	// FileName es el nombre del archivo de configuración.
	FileName = "config.json"

	// DefaultProvider es el proveedor activo en el primer arranque.
	DefaultProvider = "deepinfra"
	// DefaultSystemPrompt define el comportamiento base del asistente.
	DefaultSystemPrompt = `Eres MKD, un asistente técnico senior. Responde en el idioma del usuario, con precisión y ejemplos concretos. Usa Markdown y bloques de código cuando aporten claridad. Si falta información, dilo en lugar de inventar.`
)

// DefaultProviderOrder mantiene un orden estable para la UI y los selectores.
var DefaultProviderOrder = []string{"deepinfra", "openrouter", "openai", "codex", "ollama"}

// ProviderConfig agrupa credenciales, endpoint y modelos de un proveedor.
type ProviderConfig struct {
	Name         string   `json:"name"`
	BaseURL      string   `json:"base_url"`
	APIKey       string   `json:"api_key"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	AccountID    string   `json:"account_id,omitempty"`
	TokenExpires int64    `json:"token_expires,omitempty"`
	ActiveModel  string   `json:"active_model"`
	Models       []string `json:"models"`
}

// Modos del agente.
//
//	plan    → solo lectura (read_file, list_dir, search_code); write/patch/run se rechazan.
//	default → pide aprobación HITL para write_file, patch_file y run_command.
//	auto    → auto-aprueba escritura y comandos dentro del cwd (sandbox).
const (
	ModeChat    = "chat"
	ModePlan    = "plan"
	ModeDefault = "default"
	ModeAuto    = "auto"
)

// DefaultCompactThreshold es el umbral de tokens que dispara la
// auto-compactación cuando el usuario no configura otro valor.
const DefaultCompactThreshold = 60000

// MCPServerConfig describe un servidor MCP local lanzado sobre stdio.
//
//	{
//	  "command": "npx",
//	  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/ruta/vault"],
//	  "env": {"NOTES_DIR": "/ruta/vault"},
//	  "enabled": true
//	}
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	// AutoApprove ejecuta las tools de este servidor sin pedir permiso HITL.
	// Por defecto false: el usuario aprueba cada llamada como en las tools
	// nativas de escritura.
	AutoApprove bool `json:"auto_approve,omitempty"`
}

// SubAgentProfile describe un sub-agente del equipo Swarm.
//
//	{
//	  "id": "coder",
//	  "name": "Senior Implementer",
//	  "icon": "󰙨",
//	  "color": "#A855F7",
//	  "role_prompt": "Eres un ingeniero senior de Go…",
//	  "provider": "",
//	  "model": "",
//	  "allowed_tools": ["read_file", "search_code", "patch_file", "run_command"],
//	  "enabled": true
//	}
//
// Provider y Model vacíos heredan el proveedor/modelo activo global. Un
// AllowedTools con "mcp:*" habilita todas las tools de servidores MCP.
type SubAgentProfile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Icon         string   `json:"icon,omitempty"`
	Color        string   `json:"color,omitempty"`
	RolePrompt   string   `json:"role_prompt,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Model        string   `json:"model,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Enabled      bool     `json:"enabled"`
}

// Modos de orquestación.
//
//	single → un único agente resuelve el turno (comportamiento clásico).
//	swarm  → el agente principal actúa como Orquestador y delega en sub-agentes.
const (
	SwarmModeSingle = "single"
	SwarmModeSwarm  = "swarm"
)

// Config es la configuración raíz de MKD.
type Config struct {
	ActiveProvider   string                     `json:"active_provider"`
	SystemPrompt     string                     `json:"system_prompt"`
	CompactThreshold int                        `json:"compact_threshold,omitempty"`
	AgentMode        string                     `json:"agent_mode,omitempty"`
	SwarmMode        string                     `json:"swarm_mode,omitempty"`
	SkillsDir        string                     `json:"skills_dir,omitempty"`
	RulesDir         string                     `json:"rules_dir,omitempty"`
	Providers        map[string]ProviderConfig  `json:"providers"`
	MCPServers       map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
	SubAgents        map[string]SubAgentProfile `json:"sub_agents,omitempty"`
}

// diskConfig acepta el esquema nuevo y migra el esquema plano anterior.
type diskConfig struct {
	ActiveProvider   string                     `json:"active_provider"`
	SystemPrompt     string                     `json:"system_prompt"`
	CompactThreshold int                        `json:"compact_threshold,omitempty"`
	AgentMode        string                     `json:"agent_mode,omitempty"`
	SwarmMode        string                     `json:"swarm_mode,omitempty"`
	SkillsDir        string                     `json:"skills_dir,omitempty"`
	RulesDir         string                     `json:"rules_dir,omitempty"`
	Providers        map[string]ProviderConfig  `json:"providers"`
	MCPServers       map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
	SubAgents        map[string]SubAgentProfile `json:"sub_agents,omitempty"`

	// Campos del esquema anterior (mkd 0.1.x).
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// Defaults devuelve perfiles listos para usar. Las API keys quedan vacías.
func Defaults() Config {
	return Config{
		ActiveProvider:   DefaultProvider,
		SystemPrompt:     DefaultSystemPrompt,
		CompactThreshold: DefaultCompactThreshold,
		AgentMode:        ModeDefault,
		SwarmMode:        SwarmModeSingle,
		SubAgents:        DefaultSubAgents(),
		Providers: map[string]ProviderConfig{
			"deepinfra": {
				Name:        "DeepInfra",
				BaseURL:     "https://api.deepinfra.com/v1/openai",
				ActiveModel: "Qwen/Qwen2.5-Coder-32B-Instruct",
				Models: []string{
					"deepseek-ai/DeepSeek-V3",
					"deepseek-ai/DeepSeek-R1",
					"Qwen/Qwen2.5-Coder-32B-Instruct",
					"meta-llama/Llama-3.3-70B-Instruct-Turbo",
				},
			},
			"openrouter": {
				Name:        "OpenRouter",
				BaseURL:     "https://openrouter.ai/api/v1",
				ActiveModel: "anthropic/claude-3.5-sonnet",
				Models: []string{
					"anthropic/claude-3.5-sonnet",
					"google/gemini-2.5-pro",
					"deepseek/deepseek-chat",
					"openai/gpt-4o",
				},
			},
			"openai": {
				Name:        "OpenAI",
				BaseURL:     "https://api.openai.com/v1",
				ActiveModel: "gpt-4o",
				Models:      []string{"gpt-4o", "gpt-4o-mini", "o3-mini"},
			},
			"codex": {
				Name:        "Codex (ChatGPT)",
				BaseURL:     "https://chatgpt.com/backend-api/codex",
				ActiveModel: "gpt-5.3-codex",
				Models: []string{
					"gpt-5.3-codex",
					"gpt-4o",
					"o3-mini",
				},
			},
			"ollama": {
				Name:        "Ollama",
				BaseURL:     "http://localhost:11434/v1",
				ActiveModel: "llama3.2",
				Models:      []string{"llama3.2", "qwen2.5-coder", "deepseek-r1"},
			},
		},
		// Ejemplo deshabilitado para guiar al usuario: cámbialo a true y ajusta
		// la ruta del vault para conectar @modelcontextprotocol/server-filesystem.
		MCPServers: map[string]MCPServerConfig{
			"filesystem": {
				Command: "npx",
				Args: []string{
					"-y",
					"@modelcontextprotocol/server-filesystem",
					"/ruta/a/tu/vault",
				},
				Enabled: false,
			},
		},
	}
}

// Path devuelve la ruta absoluta del archivo de configuración.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no se pudo resolver el directorio de configuración: %w", err)
	}
	return filepath.Join(dir, AppName, FileName), nil
}

// LoadFile lee el archivo sin aplicar variables de entorno.
func LoadFile() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Defaults()
		cfg.Normalize()
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("no se pudo leer %s: %w", path, err)
	}

	var disk diskConfig
	if err := json.Unmarshal(data, &disk); err != nil {
		return Config{}, fmt.Errorf("configuración inválida en %s: %w", path, err)
	}

	cfg := fromDisk(disk)
	cfg.Normalize()
	return cfg, nil
}

// Load lee el archivo y aplica IA_* / MKD_* sobre el proveedor activo.
func Load() (Config, error) {
	cfg, err := LoadFile()
	if err != nil {
		return cfg, err
	}
	cfg.applyEnv()
	cfg.Normalize()
	return cfg, nil
}

// fromDisk convierte el JSON leído, incluyendo la migración del esquema plano.
func fromDisk(disk diskConfig) Config {
	if len(disk.Providers) > 0 {
		return Config{
			ActiveProvider:   disk.ActiveProvider,
			SystemPrompt:     disk.SystemPrompt,
			CompactThreshold: disk.CompactThreshold,
			AgentMode:        disk.AgentMode,
			SkillsDir:        disk.SkillsDir,
			RulesDir:         disk.RulesDir,
			SwarmMode:        disk.SwarmMode,
			Providers:        disk.Providers,
			MCPServers:       disk.MCPServers,
			SubAgents:        disk.SubAgents,
		}
	}

	// Sin proveedores: o es un archivo vacío o viene del esquema anterior.
	if disk.APIKey == "" && disk.BaseURL == "" && disk.Model == "" {
		cfg := Defaults()
		if strings.TrimSpace(disk.SystemPrompt) != "" {
			cfg.SystemPrompt = disk.SystemPrompt
		}
		return cfg
	}

	cfg := Defaults()
	key, name := inferProvider(disk.BaseURL)
	p := cfg.Providers[key]
	p.Name = name
	if strings.TrimSpace(disk.BaseURL) != "" {
		p.BaseURL = strings.TrimRight(strings.TrimSpace(disk.BaseURL), "/")
	}
	p.APIKey = strings.TrimSpace(disk.APIKey)
	p.ActiveModel = strings.TrimSpace(disk.Model)
	if p.ActiveModel != "" {
		p.Models = append(p.Models, p.ActiveModel)
	}
	cfg.Providers[key] = p
	cfg.ActiveProvider = key
	if strings.TrimSpace(disk.SystemPrompt) != "" {
		cfg.SystemPrompt = disk.SystemPrompt
	}
	if disk.CompactThreshold > 0 {
		cfg.CompactThreshold = disk.CompactThreshold
	}
	return cfg
}

// Save escribe la configuración de forma atómica con permisos 0600.
func Save(cfg Config) (string, error) {
	cfg.Normalize()

	path, err := Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("no se pudo crear %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("no se pudo serializar la configuración: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return "", fmt.Errorf("no se pudo crear el archivo temporal: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("no se pudieron ajustar los permisos: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("no se pudo escribir la configuración: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("no se pudo sincronizar la configuración: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("no se pudo cerrar el archivo temporal: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("no se pudo reemplazar %s: %w", path, err)
	}

	return path, nil
}

// Normalize limpia campos, deduplica modelos y garantiza un proveedor activo.
func (c *Config) Normalize() {
	c.SystemPrompt = strings.TrimSpace(c.SystemPrompt)
	if c.CompactThreshold <= 0 {
		c.CompactThreshold = DefaultCompactThreshold
	}
	c.AgentMode = NormalizeMode(c.AgentMode)

	if len(c.Providers) == 0 {
		c.Providers = Defaults().Providers
	}

	providers := make(map[string]ProviderConfig, len(c.Providers))
	for key, p := range c.Providers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			p.Name = DisplayName(key)
		}
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.APIKey = strings.TrimSpace(p.APIKey)
		p.ActiveModel = strings.TrimSpace(p.ActiveModel)
		p.Models = normalizeModels(p.Models)

		if p.ActiveModel != "" && !contains(p.Models, p.ActiveModel) {
			p.Models = append(p.Models, p.ActiveModel)
		}
		if p.ActiveModel == "" && len(p.Models) > 0 {
			p.ActiveModel = p.Models[0]
		}
		providers[key] = p
	}
	c.Providers = providers

	if c.ActiveProvider == "" {
		c.ActiveProvider = c.firstProvider()
	}
	if _, ok := c.Providers[c.ActiveProvider]; !ok {
		c.ActiveProvider = c.firstProvider()
	}

	c.MCPServers = normalizeMCPServers(c.MCPServers)
	c.SwarmMode = NormalizeSwarmMode(c.SwarmMode)
	c.SubAgents = NormalizeSubAgents(c.SubAgents)
}

// Validate comprueba las URLs configuradas. No exige API key ni modelo.
func (c Config) Validate() error {
	for key, p := range c.Providers {
		if strings.TrimSpace(p.BaseURL) == "" {
			continue
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("proveedor %s: base URL inválida %q", key, p.BaseURL)
		}
	}
	return nil
}

// Active devuelve el proveedor activo.
func (c Config) Active() (ProviderConfig, bool) {
	p, ok := c.Providers[c.ActiveProvider]
	return p, ok
}

// Configured indica si el proveedor activo puede atender peticiones.
func (c Config) Configured() bool {
	p, ok := c.Active()
	return ok && p.Configured()
}

// Configured indica si el proveedor tiene endpoint, modelo y credenciales.
func (p ProviderConfig) Configured() bool {
	if strings.TrimSpace(p.BaseURL) == "" || strings.TrimSpace(p.ActiveModel) == "" {
		return false
	}
	if IsLocal(p.BaseURL) {
		return true
	}
	if strings.TrimSpace(p.APIKey) != "" || strings.TrimSpace(p.RefreshToken) != "" {
		return true
	}
	if IsCodex(p.BaseURL) {
		return hasLocalCodexAuth()
	}
	return false
}

// hasLocalCodexAuth comprueba de forma segura si existe una sesión previa en ~/.codex/auth.json.
func hasLocalCodexAuth() bool {
	if p := os.Getenv("CODEX_HOME"); p != "" {
		if _, err := os.Stat(filepath.Join(p, "auth.json")); err == nil {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".codex", "auth.json"))
	return err == nil
}

// IsCodex indica si la URL o el proveedor apunta al backend de suscripción de Codex/ChatGPT.
func IsCodex(baseURL string) bool {
	lower := strings.ToLower(baseURL)
	return strings.Contains(lower, "chatgpt.com/backend-api/codex") || strings.Contains(lower, "codex")
}

// Provider devuelve el nombre legible del proveedor activo.
func (c Config) Provider() string {
	p, ok := c.Active()
	if !ok || strings.TrimSpace(p.Name) == "" {
		return "sin configurar"
	}
	return p.Name
}

// ProviderKeys devuelve las claves en orden estable (conocidos primero).
func (c Config) ProviderKeys() []string {
	seen := make(map[string]bool, len(c.Providers))
	keys := make([]string, 0, len(c.Providers))
	for _, key := range DefaultProviderOrder {
		if _, ok := c.Providers[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	extra := make([]string, 0)
	for key := range c.Providers {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return append(keys, extra...)
}

// IsLocal indica si la URL apunta a una instancia local (Ollama, LM Studio...).
func IsLocal(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// MaskKey enmascara una API key para mostrarla en UI y CLI.
func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "(sin configurar)"
	}
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	head := 3
	if len(key) < head+4 {
		head = 2
	}
	return key[:head] + "••••" + key[len(key)-4:]
}

// DisplayName convierte una clave de proveedor en un nombre legible.
func DisplayName(key string) string {
	switch key {
	case "deepinfra":
		return "DeepInfra"
	case "openrouter":
		return "OpenRouter"
	case "openai":
		return "OpenAI"
	case "codex":
		return "Codex (ChatGPT)"
	case "ollama":
		return "Ollama"
	default:
		if key == "" {
			return "Sin configurar"
		}
		return key
	}
}

func (c Config) firstProvider() string {
	keys := c.ProviderKeys()
	if len(keys) == 0 {
		return DefaultProvider
	}
	return keys[0]
}

func inferProvider(baseURL string) (key, name string) {
	lower := strings.ToLower(baseURL)
	switch {
	case strings.Contains(lower, "deepinfra"):
		return "deepinfra", "DeepInfra"
	case strings.Contains(lower, "openrouter"):
		return "openrouter", "OpenRouter"
	case strings.Contains(lower, "chatgpt.com/backend-api/codex") || strings.Contains(lower, "codex"):
		return "codex", "Codex (ChatGPT)"
	case strings.Contains(lower, "api.openai.com"):
		return "openai", "OpenAI"
	case IsLocal(baseURL):
		return "ollama", "Ollama"
	default:
		return "custom", "Custom"
	}
}

func (c *Config) applyEnv() {
	if v, ok := envValue("IA_PROVIDER", "MKD_PROVIDER"); ok {
		c.ActiveProvider = v
	}

	key := c.ActiveProvider
	p, ok := c.Providers[key]
	if !ok {
		p = ProviderConfig{Name: DisplayName(key)}
	}

	changed := false
	if v, ok := envValue("IA_BASE_URL", "MKD_BASE_URL"); ok {
		p.BaseURL = v
		changed = true
	}
	if v, ok := envValue("IA_API_KEY", "MKD_API_KEY"); ok {
		p.APIKey = v
		changed = true
	}
	if v, ok := envValue("IA_MODEL", "MKD_MODEL"); ok {
		p.ActiveModel = v
		changed = true
	}
	if changed {
		c.Providers[key] = p
	}
	if v, ok := envValue("IA_SYSTEM_PROMPT", "IA_SYSTEM", "MKD_SYSTEM_PROMPT", "MKD_SYSTEM"); ok {
		c.SystemPrompt = v
	}
	if v, ok := envValue("IA_RULES_DIR", "MKD_RULES_DIR"); ok {
		c.RulesDir = v
	}
	if v, ok := envValue("IA_SKILLS_DIR", "MKD_SKILLS_DIR"); ok {
		c.SkillsDir = v
	}
	if v, ok := envValue("IA_AGENT_MODE", "MKD_AGENT_MODE"); ok {
		c.AgentMode = NormalizeMode(v)
	}
	if v, ok := envValue("IA_COMPACT_THRESHOLD", "MKD_COMPACT_THRESHOLD"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.CompactThreshold = n
		}
	}
}

func envValue(keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Sub-agentes (Swarm)
// ---------------------------------------------------------------------------

// Sub-agent códigos preinstalados. Deben coincidir con internal/swarm.
const (
	SubAgentExplorer = "explorer"
	SubAgentCoder    = "coder"
	SubAgentQA       = "qa"
	SubAgentDocs     = "docs"
)

// NormalizeSwarmMode valida el modo de orquestación.
func NormalizeSwarmMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), SwarmModeSwarm) {
		return SwarmModeSwarm
	}
	return SwarmModeSingle
}

// NextSwarmMode alterna single ↔ swarm.
func NextSwarmMode(mode string) string {
	if NormalizeSwarmMode(mode) == SwarmModeSwarm {
		return SwarmModeSingle
	}
	return SwarmModeSwarm
}

// SwarmModeLabel devuelve una etiqueta legible para la barra de estado.
func SwarmModeLabel(mode string) string {
	if NormalizeSwarmMode(mode) == SwarmModeSwarm {
		return "SWARM"
	}
	return "SINGLE"
}

// DefaultSubAgents devuelve el equipo por defecto (4 perfiles editables).
func DefaultSubAgents() map[string]SubAgentProfile {
	profiles := []SubAgentProfile{
		{
			ID:    SubAgentExplorer,
			Name:  "Code Explorer",
			Icon:  "󰀐",
			Color: "#06B6D4",
			RolePrompt: "Eres un Code Explorer senior. Mapea la arquitectura del proyecto, localiza " +
				"definiciones y referencias con search_code y lee archivos con read_file. NO modifiques " +
				"código ni ejecutes comandos. Entrega un informe conciso con rutas y símbolos clave.",
			AllowedTools: []string{"read_file", "list_dir", "search_code"},
			Enabled:      true,
		},
		{
			ID:    SubAgentCoder,
			Name:  "Senior Implementer",
			Icon:  "󰙨",
			Color: "#A855F7",
			RolePrompt: "Eres un ingeniero senior experto en Go. Escribes código idiomático, modular y " +
				"con errores manejados explícitamente. Investiga primero, aplica parches quirúrgicos con " +
				"patch_file y usa write_file solo para archivos nuevos o reescrituras completas.",
			AllowedTools: []string{"read_file", "list_dir", "search_code", "patch_file", "write_file", "run_command"},
			Enabled:      true,
		},
		{
			ID:    SubAgentQA,
			Name:  "QA & Security Auditor",
			Icon:  "󰒋",
			Color: "#F59E0B",
			RolePrompt: "Eres un auditor de QA y seguridad. Ejecutas tests y builds con run_command y " +
				"revisas bugs, condiciones de carrera, fugas de recursos y vulnerabilidades. Reporta " +
				"hallazgos con severidad y evidencia (archivo:línea). No apliques fixes salvo que se pida.",
			AllowedTools: []string{"read_file", "list_dir", "search_code", "run_command"},
			Enabled:      true,
		},
		{
			ID:    SubAgentDocs,
			Name:  "Tech Writer",
			Icon:  "󰈙",
			Color: "#10B981",
			RolePrompt: "Eres un redactor técnico. Documentas arquitectura, APIs y flujos con Markdown " +
				"claro, diagramas ASCII o Mermaid y ejemplos. Puedes leer código y escribir documentación.",
			AllowedTools: []string{"read_file", "list_dir", "search_code", "write_file"},
			Enabled:      false,
		},
	}
	out := make(map[string]SubAgentProfile, len(profiles))
	for _, profile := range profiles {
		out[profile.ID] = profile
	}
	return out
}

// NormalizeSubAgents limpia IDs, colores y tools, y siembra el equipo por
// defecto cuando el mapa está vacío.
func NormalizeSubAgents(agents map[string]SubAgentProfile) map[string]SubAgentProfile {
	if len(agents) == 0 {
		return DefaultSubAgents()
	}
	out := make(map[string]SubAgentProfile, len(agents))
	for key, profile := range agents {
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			id = strings.TrimSpace(key)
		}
		if id == "" {
			continue
		}
		profile.ID = id
		profile.Name = strings.TrimSpace(profile.Name)
		if profile.Name == "" {
			profile.Name = id
		}
		profile.Icon = strings.TrimSpace(profile.Icon)
		if profile.Icon == "" {
			profile.Icon = "󰀐"
		}
		profile.Color = NormalizeAgentColor(profile.Color)
		profile.Provider = strings.TrimSpace(profile.Provider)
		profile.Model = strings.TrimSpace(profile.Model)
		profile.AllowedTools = normalizeAgentTools(profile.AllowedTools)
		out[id] = profile
	}
	if len(out) == 0 {
		return DefaultSubAgents()
	}
	return out
}

// NormalizeAgentColor valida #RRGGBB y cae a cian si es inválido.
func NormalizeAgentColor(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 7 && value[0] == '#' {
		valid := true
		for _, r := range value[1:] {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				valid = false
				break
			}
		}
		if valid {
			return strings.ToUpper(value)
		}
	}
	return "#06B6D4"
}

func normalizeAgentTools(tools []string) []string {
	if len(tools) == 0 {
		return []string{"*"}
	}
	seen := make(map[string]bool, len(tools))
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" || seen[tool] {
			continue
		}
		seen[tool] = true
		out = append(out, tool)
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

// normalizeMCPServers limpia comandos, argumentos y variables de entorno.
func normalizeMCPServers(servers map[string]MCPServerConfig) map[string]MCPServerConfig {
	if len(servers) == 0 {
		return servers
	}
	out := make(map[string]MCPServerConfig, len(servers))
	for name, server := range servers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		server.Command = strings.TrimSpace(server.Command)
		args := make([]string, 0, len(server.Args))
		for _, arg := range server.Args {
			args = append(args, strings.TrimSpace(arg))
		}
		server.Args = args
		if len(server.Env) > 0 {
			env := make(map[string]string, len(server.Env))
			for key, value := range server.Env {
				key = strings.TrimSpace(key)
				if key == "" {
					continue
				}
				env[key] = strings.TrimSpace(value)
			}
			server.Env = env
		}
		out[name] = server
	}
	return out
}

func normalizeModels(models []string) []string {
	seen := make(map[string]bool, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		result = append(result, model)
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// NormalizeMode valida el modo del agente y cae a "default" si es desconocido.
func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModePlan:
		return ModePlan
	case ModeAuto:
		return ModeAuto
	case ModeChat:
		return ModeChat
	default:
		return ModeDefault
	}
}

// NextMode rota entre los modos en el orden plan → default → auto → plan.
func NextMode(mode string) string {
	switch NormalizeMode(mode) {
	case ModePlan:
		return ModeDefault
	case ModeAuto:
		return ModePlan
	case ModeChat:
		return ModeDefault
	default:
		return ModeAuto
	}
}

// ModeLabel devuelve un nombre legible del modo para la barra de estado.
func ModeLabel(mode string) string {
	switch NormalizeMode(mode) {
	case ModePlan:
		return "PLAN"
	case ModeAuto:
		return "AUTO"
	case ModeChat:
		return "CHAT"
	default:
		return "DEFAULT"
	}
}
