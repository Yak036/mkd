// Package context ensambla los mensajes enviados al proveedor y gestiona la
// compactación del historial.
//
// El ensamblado mantiene un prefijo estable para aprovechar el Prefix Prompt
// Caching de DeepInfra/OpenRouter:
//
//	┌────────────────────────────┐
//	│ 1. system estático         │ ◄── nunca cambia entre peticiones
//	│    base + MKD.md           │
//	├────────────────────────────┤
//	│ 2. <context_files>…</…>    │ ◄── cambia solo si cambia un archivo
//	├────────────────────────────┤
//	│ 3. resumen de /compact     │ ◄── cambia solo al compactar
//	├────────────────────────────┤
//	│ 4. historial user/asst     │ ◄── append-only
//	└────────────────────────────┘
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"mkd/internal/ai"
	"mkd/internal/rules"
)

// ContextDocName es el archivo que se anexa al system prompt si existe en el
// directorio de trabajo actual.
const ContextDocName = "MKD.md"

// PinnedFile es un archivo anclado al contexto, con su estado de caché.
type PinnedFile struct {
	Path string
	Size int64
	Hash string
	Err  string
}

// TokenUsage desglosa la estimación de tokens de la ventana actual.
type TokenUsage struct {
	SystemTokens  int
	FileTokens    int
	SummaryTokens int
	HistoryTokens int
	TotalTokens   int
}

// Prepared es el resultado de preparar el contexto para una petición.
type Prepared struct {
	System  string
	Files   string
	Summary string
	History []ai.Message
}

// Usage es la instantánea del uso de tokens del último ensamblado.
type Usage struct {
	TokenUsage
	Files []PinnedFile
}

// Manager ensambla mensajes y mantiene la caché de archivos anclados.
type Manager struct {
	SystemPrompt string
	WorkDir      string
	MaxTokens    int

	// RulesDir es la carpeta global de reglas (vacío = ~/.config/mkd/rules).
	RulesDir string
	// ManualRules son nombres forzados con `:@rule-nombre`.
	ManualRules []string

	files     []PinnedFile
	doc       string
	docLoaded bool

	rules          []rules.Rule
	rulesLoaded    bool
	rulesSelection rules.Selection
}

// NewManager construye un Manager para el directorio de trabajo dado.
func NewManager(systemPrompt, workDir string, maxTokens int) *Manager {
	if strings.TrimSpace(workDir) == "" {
		workDir, _ = os.Getwd()
	}
	if maxTokens <= 0 {
		maxTokens = DefaultCompactThreshold
	}
	return &Manager{
		SystemPrompt: systemPrompt,
		WorkDir:      workDir,
		MaxTokens:    maxTokens,
	}
}

// SetRulesDir actualiza la carpeta global de reglas e invalida la caché.
func (m *Manager) SetRulesDir(dir string) {
	if m.RulesDir == dir {
		return
	}
	m.RulesDir = dir
	m.rulesLoaded = false
}

// SetManualRules fija las reglas forzadas por el usuario.
func (m *Manager) SetManualRules(names []string) {
	m.ManualRules = append([]string(nil), names...)
}

// Rules devuelve las reglas descubiertas (reléelas si la caché expiró).
func (m *Manager) Rules() []rules.Rule {
	m.loadRules()
	return m.rules
}

// RuleSelection expone la última selección calculada (para el sidebar).
func (m *Manager) RuleSelection() rules.Selection {
	return m.rulesSelection
}

// loadRules descubre las reglas una sola vez por Manager.
func (m *Manager) loadRules() {
	if m.rulesLoaded {
		return
	}
	m.rulesLoaded = true
	list, err := rules.Load(m.RulesDir, m.WorkDir)
	if err != nil {
		m.rules = nil
		return
	}
	m.rules = list
}

// SetSystemPrompt actualiza el system prompt base (y relee MKD.md).
func (m *Manager) SetSystemPrompt(prompt string) {
	if m.SystemPrompt == prompt {
		return
	}
	m.SystemPrompt = prompt
	m.docLoaded = false
}

// SetMaxTokens ajusta el umbral de auto-compactación.
func (m *Manager) SetMaxTokens(maxTokens int) {
	if maxTokens > 0 {
		m.MaxTokens = maxTokens
	}
}

// Multiplier devuelve el factor de uso respecto al umbral configurado.
func (m *Manager) Multiplier(usage TokenUsage) float64 {
	if m.MaxTokens <= 0 {
		return 0
	}
	return float64(usage.TotalTokens) / float64(m.MaxTokens)
}

// AddFile ancla un archivo. Relee el disco para calcular el hash SHA-256.
func (m *Manager) AddFile(path string) (PinnedFile, error) {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return PinnedFile{}, fmt.Errorf("ruta vacía")
	}
	file := readPinnedFile(path)
	if file.Err != "" {
		return file, fmt.Errorf("%s", file.Err)
	}

	for i := range m.files {
		if m.files[i].Path == path {
			m.files[i] = file
			return file, nil
		}
	}
	m.files = append(m.files, file)
	return file, nil
}

// RemoveFile quita un archivo anclado.
func (m *Manager) RemoveFile(path string) {
	filtered := m.files[:0]
	for _, file := range m.files {
		if file.Path != path {
			filtered = append(filtered, file)
		}
	}
	m.files = filtered
}

// Files devuelve una copia de los archivos anclados con su último estado.
func (m *Manager) Files() []PinnedFile {
	out := make([]PinnedFile, len(m.files))
	copy(out, m.files)
	return out
}

// RefreshFiles relee los archivos del disco, actualizando hash y detectando
// cambios. Devuelve las rutas cuyo contenido cambió.
func (m *Manager) RefreshFiles() []string {
	changed := make([]string, 0, len(m.files))
	for i := range m.files {
		current := m.files[i]
		next := readPinnedFile(current.Path)
		if next.Err == "" && current.Err == "" && next.Hash != current.Hash {
			changed = append(changed, next.Path)
		}
		if next.Err != "" && current.Err == "" {
			changed = append(changed, next.Path)
		}
		m.files[i] = next
	}
	return changed
}

// Prepare ensambla system, bloque de archivos, resumen e historial y devuelve
// también el desglose de tokens.
func (m *Manager) Prepare(summary string, history []ai.Message) (Prepared, Usage) {
	system := m.systemBlock()
	filesBlock := m.filesBlock()

	var changed []string
	m.refreshSilently(&filesBlock, &changed)

	prepared := Prepared{
		System:  system,
		Files:   filesBlock,
		Summary: strings.TrimSpace(summary),
		History: history,
	}

	usage := Usage{Files: m.Files()}
	usage.SystemTokens = EstimateTokens(system)
	usage.FileTokens = EstimateTokens(filesBlock)
	usage.SummaryTokens = EstimateTokens(prepared.Summary)
	for _, msg := range history {
		usage.HistoryTokens += EstimateTokens(msg.Content) + 4
	}
	usage.TotalTokens = usage.SystemTokens + usage.FileTokens + usage.SummaryTokens + usage.HistoryTokens
	return prepared, usage
}

// Messages convierte un ensamblado en el slice que consume la API.
func (p Prepared) Messages() []openai.ChatCompletionMessage {
	messages := make([]openai.ChatCompletionMessage, 0, len(p.History)+2)
	if strings.TrimSpace(p.System) != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: p.System,
		})
	}
	if strings.TrimSpace(p.Files) != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: p.Files,
		})
	}
	if strings.TrimSpace(p.Summary) != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: "<context_summary>\n" + p.Summary + "\n</context_summary>",
		})
	}
	for _, msg := range p.History {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return messages
}

// systemBlock devuelve el system prompt base más MKD.md si existe. La lectura
// se cachea: solo se invalida cuando cambia el system prompt, para no meter
// datos dinámicos que rompan la caché de prefijo.
func (m *Manager) systemBlock() string {
	base := strings.TrimSpace(m.SystemPrompt)
	if !m.docLoaded {
		m.docLoaded = true
		m.doc = ""
		if m.WorkDir != "" {
			path := filepath.Join(m.WorkDir, ContextDocName)
			if data, err := os.ReadFile(path); err == nil {
				if text := strings.TrimSpace(string(data)); text != "" {
					m.doc = "<project_context path=\"" + ContextDocName + "\">\n" + text + "\n</project_context>"
				}
			}
		}
	}
	parts := make([]string, 0, 3)
	if base != "" {
		parts = append(parts, base)
	}
	if m.doc != "" {
		parts = append(parts, m.doc)
	}
	if block := m.rulesBlock(); block != "" {
		parts = append(parts, block)
	}
	return strings.Join(parts, "\n\n")
}

// rulesBlock selecciona las reglas aplicables a los archivos anclados y las
// renderiza como <project_rules>. También guarda la selección para la UI.
func (m *Manager) rulesBlock() string {
	m.loadRules()
	touched := make([]string, 0, len(m.files))
	for _, file := range m.files {
		if file.Path != "" && file.Err == "" {
			touched = append(touched, file.Path)
		}
	}
	m.rulesSelection = rules.Select(m.rules, touched, m.ManualRules)
	return rules.Block(m.rulesSelection.Rules)
}

func (m *Manager) refreshSilently(filesBlock *string, changed *[]string) {
	if len(m.files) == 0 {
		return
	}
	updated := m.RefreshFiles()
	if len(updated) == 0 {
		return
	}
	*changed = updated
	*filesBlock = m.filesBlock()
}

func (m *Manager) filesBlock() string {
	if len(m.files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<context_files>\n")
	for _, file := range m.files {
		if file.Err != "" {
			fmt.Fprintf(&b, "<file path=%q unavailable=%q/>\n", file.Path, file.Err)
			continue
		}
		data, err := os.ReadFile(file.Path)
		if err != nil {
			fmt.Fprintf(&b, "<file path=%q unavailable=%q/>\n", file.Path, err.Error())
			continue
		}
		fmt.Fprintf(&b, "<file path=%q sha256=%q>\n", file.Path, file.Hash)
		b.Write(data)
		if !strings.HasSuffix(string(data), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("</file>\n")
	}
	b.WriteString("</context_files>")
	return b.String()
}

func readPinnedFile(path string) PinnedFile {
	file := PinnedFile{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		file.Err = err.Error()
		return file
	}
	if info.IsDir() {
		file.Err = "es un directorio"
		return file
	}
	if info.Size() > MaxFileBytes {
		file.Err = fmt.Sprintf("supera %d MiB", MaxFileBytes>>20)
		return file
	}
	data, err := os.ReadFile(path)
	if err != nil {
		file.Err = err.Error()
		return file
	}
	sum := sha256.Sum256(data)
	file.Size = info.Size()
	file.Hash = hex.EncodeToString(sum[:])
	return file
}
