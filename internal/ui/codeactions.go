package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Yak036/mkd/internal/clipboard"
)

// ---------------------------------------------------------------------------
// Extracción de bloques de código desde mensajes markdown
// ---------------------------------------------------------------------------

// CodeBlock representa un bloque de código detectado en una respuesta.
//
//	```go                      ◄── Lang
//	// path/to/file.go          ◄── Path (comentario inicial)
//	package main ...           ◄── Content
//	```                        ◄── StartLine = línea de la primera tilde
type CodeBlock struct {
	Index     int    // número visible en el modal (1-based)
	Lang      string // lenguaje declarado en la tilde de apertura
	Content   string // contenido limpio, sin tildes ni comentario de ruta
	Path      string // ruta sugerida (vacía si no se detecta)
	StartLine int    // línea (1-based) donde abre la tilde
}

// blockFence detecta tildes de 3 o más backticks con lenguaje opcional.
// El grupo 1 es la tilde literal; el grupo 2, el lenguaje.
var blockFence = regexp.MustCompile("^[ \t]*(`{3,})[ \t]*([A-Za-z0-9_+.#-]*)")

// blockHeader detecta una cabecera markdown inmediatamente anterior al bloque.
var blockHeaders = []*regexp.Regexp{
	// **Archivo:** `ruta` / **File:** `ruta` / **Path:** `ruta`
	regexp.MustCompile(`(?i)\*\*(?:archivo|file|path|ruta)\s*:?\*\*[ \t]*[` + "`" + `]([^` + "`" + `]+)[` + "`" + `]`),
	// ### `ruta` / ### ruta.ext
	regexp.MustCompile(`^[ \t]*#{1,6}[ \t]+[` + "`" + `]?([^\s` + "`" + `]+)[` + "`" + `]?[ \t]*$`),
	// ruta.ext a secas
	regexp.MustCompile(`^[ \t]*[` + "`" + `]?([^\s` + "`" + `]+\.[A-Za-z0-9]+)[` + "`" + `]?[ \t]*$`),
}

// pathExtensions son las extensiones que aceptamos como "ruta probable" cuando
// una línea suelta parece un nombre de archivo.
var pathExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
	".jsx": true, ".sh": true, ".bash": true, ".zsh": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true, ".rs": true, ".sql": true,
	".html": true, ".css": true, ".scss": true, ".md": true, ".java": true,
	".kt": true, ".rb": true, ".php": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cs": true, ".swift": true, ".dockerfile": true,
}

// ExtractCodeBlocks recorre los mensajes en orden y devuelve todos los bloques
// cerrados (o abiertos al final de un mensaje, que se toman hasta EOF).
func ExtractCodeBlocks(messages []string) []CodeBlock {
	var blocks []CodeBlock

	for _, message := range messages {
		lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
		for i := 0; i < len(lines); i++ {
			match := blockFence.FindStringSubmatch(lines[i])
			if match == nil {
				continue
			}
			fence := match[1] // tilde literal con la que abrió
			lang := strings.TrimSpace(match[2])

			// Busca la tilde de cierre: misma longitud o superior y sin info.
			end := -1
			for j := i + 1; j < len(lines); j++ {
				closeMatch := blockFence.FindStringSubmatch(lines[j])
				if closeMatch == nil {
					continue
				}
				if len(closeMatch[1]) >= len(fence) && strings.TrimSpace(closeMatch[2]) == "" {
					end = j
					break
				}
			}

			bodyStart := i + 1
			bodyEnd := len(lines)
			if end >= 0 {
				bodyEnd = end
			}

			content := lines[bodyStart:bodyEnd]
			header := strings.Join(lines[max(0, i-3):i], "\n")
			content, path := cleanBlock(content, header, lang)

			blocks = append(blocks, CodeBlock{
				Index:     len(blocks) + 1,
				Lang:      lang,
				Content:   strings.Join(content, "\n"),
				Path:      path,
				StartLine: i + 1,
			})

			if end >= 0 {
				i = end
			} else {
				i = len(lines)
			}
		}
	}
	return blocks
}

// cleanBlock quita el comentario de ruta inicial y limpia backticks sueltos.
// Devuelve el contenido y la ruta detectada (si existe).
func cleanBlock(content []string, header, lang string) ([]string, string) {
	path := ""

	// Comentario inicial dentro del bloque: // ruta, # ruta, -- ruta, <!-- ruta -->
	if len(content) > 0 {
		if candidate := blockCommentPath(content[0], lang); candidate != "" {
			path = candidate
			content = content[1:]
		}
	}

	// Cabecera inmediatamente anterior: solo si no hubo ruta en comentario.
	if path == "" && header != "" {
		if candidate := headerPath(strings.Split(header, "\n")); candidate != "" {
			path = candidate
		}
	}

	// Limpia tildes residuales y espacios al borde.
	for i := range content {
		content[i] = strings.TrimRight(strings.ReplaceAll(content[i], "\r", ""), " \t")
	}
	// Recorta líneas vacías al inicio/fin.
	for len(content) > 0 && strings.TrimSpace(content[0]) == "" {
		content = content[1:]
	}
	for len(content) > 0 && strings.TrimSpace(content[len(content)-1]) == "" {
		content = content[:len(content)-1]
	}

	return content, path
}

// blockCommentPath extrae una ruta desde la primera línea si es un comentario.
func blockCommentPath(line, lang string) string {
	trimmed := strings.TrimSpace(line)

	var value string
	switch {
	case strings.HasPrefix(trimmed, "//"):
		value = strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
	case strings.HasPrefix(trimmed, "--"):
		value = strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))
	case strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->"):
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "<!--"), "-->"))
	case strings.HasPrefix(trimmed, "#"):
		// # en YAML/shell/Python es comentario; en Go/etc. no.
		if isHashCommentLang(lang) {
			value = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		}
	}

	if value == "" || !looksLikePath(value) {
		return ""
	}
	return value
}

// isHashCommentLang indica si el lenguaje usa # para comentarios.
func isHashCommentLang(lang string) bool {
	switch strings.ToLower(lang) {
	case "bash", "sh", "shell", "zsh", "fish", "python", "py", "yaml", "yml",
		"toml", "rb", "ruby", "perl", "makefile", "dockerfile", "":
		return true
	}
	return false
}

// headerPath busca una ruta en las líneas de cabecera (últimas 4).
func headerPath(header []string) string {
	// Recorre desde la cabecera más cercana al bloque.
	for i := len(header) - 1; i >= 0; i-- {
		line := header[i]
		for _, re := range blockHeaders {
			match := re.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			candidate := strings.TrimSpace(match[1])
			if !looksLikePath(candidate) {
				continue
			}
			// La regla de "línea suelta" exige una línea vacía después;
			// la comprobamos para evitar falsos positivos dentro de prosa.
			if i < len(header)-1 && strings.TrimSpace(header[i+1]) != "" && looksLikeSentence(line) {
				continue
			}
			return candidate
		}
	}
	return ""
}

// looksLikePath valida que el candidato tenga forma de ruta relativa o nombre
// de archivo con extensión conocida.
func looksLikePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(value))
	if ext == "" {
		return false
	}
	return pathExtensions[ext] || len(ext) <= 5
}

// looksLikeSentence detecta prosa que casualmente termina en algo con puntos.
func looksLikeSentence(line string) bool {
	return strings.ContainsAny(line, " áéíóúñ,;:")
}

// BlockLines cuenta las líneas no vacías del contenido de un bloque.
func BlockLines(b CodeBlock) int {
	count := 0
	for _, line := range strings.Split(b.Content, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// lastCodeBlock devuelve el último bloque detectado.
func lastCodeBlock(blocks []CodeBlock) (CodeBlock, bool) {
	if len(blocks) == 0 {
		return CodeBlock{}, false
	}
	return blocks[len(blocks)-1], true
}

// langExtension mapea el lenguaje del bloque a una extensión de archivo.
func langExtension(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return ".go"
	case "bash", "sh", "shell", "zsh", "fish", "console", "shell-session":
		return ".sh"
	case "json", "jsonc":
		return ".json"
	case "yaml", "yml":
		return ".yml"
	case "python", "py", "python3":
		return ".py"
	case "javascript", "js", "node", "mjs", "cjs":
		return ".js"
	case "typescript", "ts":
		return ".ts"
	case "tsx":
		return ".tsx"
	case "jsx":
		return ".jsx"
	case "rust", "rs":
		return ".rs"
	case "sql":
		return ".sql"
	case "html", "htm":
		return ".html"
	case "css":
		return ".css"
	case "scss", "sass":
		return ".scss"
	case "markdown", "md":
		return ".md"
	case "java":
		return ".java"
	case "kotlin", "kt":
		return ".kt"
	case "ruby", "rb":
		return ".rb"
	case "php":
		return ".php"
	case "c":
		return ".c"
	case "cpp", "c++", "cxx":
		return ".cpp"
	case "csharp", "cs", "c#":
		return ".cs"
	case "swift":
		return ".swift"
	case "toml":
		return ".toml"
	case "dockerfile", "docker":
		return ".dockerfile"
	case "xml":
		return ".xml"
	case "ini", "conf":
		return ".conf"
	default:
		return ""
	}
}

// isShellLang indica si el bloque puede ejecutarse como shell.
func isShellLang(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "bash", "sh", "shell", "zsh":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Modal selector de bloques
// ---------------------------------------------------------------------------

// blockAction es la acción tipada que el modal devuelve al workspace para que
// este decida qué hacer (copiar, guardar o ejecutar). El modal nunca ejecuta
// comandos por sí mismo.
type blockAction struct {
	Kind  string // "copy" | "save" | "run"
	Block CodeBlock
	Path  string
}

const (
	blockActionCopy = "copy"
	blockActionSave = "save"
	blockActionRun  = "run"
)

// blocksModal muestra los bloques detectados y permite copiar/guardar/ejecutar.
type blocksModal struct {
	blocks   []CodeBlock
	selected int
	scroll   int
	status   string
	err      string
}

// openBlocksModal reconstruye la lista desde el historial de la sesión.
// No está conectado al router todavía; el workspace lo invocará.
func (m Model) openBlocksModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalBlocks
	contents := make([]string, 0, len(m.history))
	for _, msg := range m.history {
		contents = append(contents, msg.Content)
	}
	blocks := ExtractCodeBlocks(contents)
	modal := blocksModal{blocks: blocks}
	if len(blocks) == 0 {
		modal.status = "No se detectaron bloques de código en la conversación."
	}
	m.modal.blocks = modal
	m.textarea.Blur()
	m.resizeModals()
	return m, nil
}

// updateBlocksModal procesa las teclas del selector.
func (m Model) updateBlocksModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	bm := &m.modal.blocks

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "up":
		bm.moveSelection(-1, m.blocksRows())
		return m, nil
	case msg.String() == "down":
		bm.moveSelection(1, m.blocksRows())
		return m, nil
	case msg.String() == "pgup":
		bm.moveSelection(-m.blocksRows(), m.blocksRows())
		return m, nil
	case msg.String() == "pgdown":
		bm.moveSelection(m.blocksRows(), m.blocksRows())
		return m, nil
	}

	// Selección directa con 1-9: mueve y copia.
	if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
		index := int(msg.Runes[0] - '1')
		if index < len(bm.blocks) {
			bm.selected = index
			bm.ensureVisible(m.blocksRows())
			note, err := copyBlock(bm.blocks[index])
			bm.applyResult(note, err)
		}
		return m, nil
	}

	switch msg.String() {
	case "enter":
		if block, ok := bm.selectedBlock(); ok {
			note, err := copyBlock(block)
			bm.applyResult(note, err)
		}
		return m, nil
	case "ctrl+s":
		return m.saveSelectedBlock()
	case "ctrl+r":
		if block, ok := bm.selectedBlock(); ok {
			if !isShellLang(block.Lang) {
				bm.status = "Solo se ejecutan bloques bash/sh/shell."
				bm.err = ""
				return m, nil
			}
			bm.status = "Acción lista: ejecutar " + blockLabel(block)
			bm.err = ""
		}
		return m, nil
	}
	return m, nil
}

// saveSelectedBlock escribe el bloque bajo el cwd con os.WriteFile 0644.
func (m Model) saveSelectedBlock() (Model, tea.Cmd) {
	bm := &m.modal.blocks
	block, ok := bm.selectedBlock()
	if !ok {
		return m, nil
	}

	path := block.Path
	if strings.TrimSpace(path) == "" {
		ext := langExtension(block.Lang)
		if ext == "" {
			ext = ".txt"
		}
		path = fmt.Sprintf("bloque-%d%s", block.Index, ext)
	}

	// Nunca escapamos del cwd con rutas absolutas detectadas.
	if filepath.IsAbs(path) {
		path = filepath.Base(path)
	}

	if err := os.WriteFile(path, []byte(block.Content+"\n"), 0o644); err != nil {
		bm.err = err.Error()
		bm.status = ""
		return m, nil
	}
	bm.err = ""
	bm.status = fmt.Sprintf("✓ Guardado en %s (%d líneas)", path, BlockLines(block))
	return m, nil
}

// copyBlock copia el contenido al portapapeles y devuelve el texto de aviso.
func copyBlock(block CodeBlock) (string, error) {
	if err := clipboard.Copy(block.Content); err != nil {
		return "", err
	}
	return fmt.Sprintf("✓ Bloque copiado al portapapeles (%d líneas)", BlockLines(block)), nil
}

// applyResult guarda el estado de la última operación en el modal.
func (bm *blocksModal) applyResult(note string, err error) {
	if err != nil {
		bm.err = err.Error()
		bm.status = ""
		return
	}
	bm.err = ""
	bm.status = note
}

// selectedBlock devuelve el bloque resaltado.
func (bm *blocksModal) selectedBlock() (CodeBlock, bool) {
	if len(bm.blocks) == 0 {
		return CodeBlock{}, false
	}
	index := bm.selected
	if index < 0 {
		index = 0
	}
	if index >= len(bm.blocks) {
		index = len(bm.blocks) - 1
	}
	return bm.blocks[index], true
}

// moveSelection desplaza el cursor manteniendo el scroll visible.
func (bm *blocksModal) moveSelection(delta, rows int) {
	if len(bm.blocks) == 0 {
		return
	}
	bm.selected += delta
	if bm.selected < 0 {
		bm.selected = 0
	}
	if bm.selected >= len(bm.blocks) {
		bm.selected = len(bm.blocks) - 1
	}
	bm.ensureVisible(rows)
}

// ensureVisible ajusta el scroll a la fila seleccionada.
func (bm *blocksModal) ensureVisible(rows int) {
	if rows <= 0 || len(bm.blocks) == 0 {
		return
	}
	if bm.selected < bm.scroll {
		bm.scroll = bm.selected
	}
	if bm.selected >= bm.scroll+rows {
		bm.scroll = bm.selected - rows + 1
	}
	maxScroll := max(0, len(bm.blocks)-rows)
	if bm.scroll > maxScroll {
		bm.scroll = maxScroll
	}
}

// blocksRows calcula cuántas filas de bloques caben en el modal.
func (m Model) blocksRows() int {
	return min(8, max(2, (m.modalContentHeight()-12)/1))
}

// blocksModalView dibuja el selector.
//
//	┌─ 󰅩 Bloques de Código Detectados ───────────────┐
//	│ [1] go     │ main.go        (42 líneas)         │
//	│ [2] bash   │ deploy.sh      (12 líneas)         │
//	│ ──────────────────────────────────────────      │
//	│   packet main                                   │
//	│   ...                                           │
//	│ ↑/↓ mover · 1-9 copiar · Enter copiar          │
//	└─────────────────────────────────────────────────┘
func (m Model) blocksModalView() string {
	bm := m.modal.blocks
	var b strings.Builder

	b.WriteString(titleStyle.Render("󰅩 Bloques de Código Detectados"))
	b.WriteString("\n\n")

	if len(bm.blocks) == 0 {
		b.WriteString(warnStyle.Render("No hay bloques de código disponibles."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Pide al modelo que incluya código en ``` y vuelve a intentar."))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("Esc cerrar"))
		return m.modalBox(b.String())
	}

	rows := m.blocksRows()
	bm.ensureVisible(rows)
	start := bm.scroll
	end := min(len(bm.blocks), start+rows)
	for i := start; i < end; i++ {
		block := bm.blocks[i]
		row := fmt.Sprintf("[%d] %-8s │ %s (%d líneas)",
			block.Index,
			valueOr(block.Lang, "text"),
			valueOr(block.Path, "(ruta no detectada)"),
			BlockLines(block),
		)
		if i == bm.selected {
			b.WriteString(selectedRowStyle.Render(ansi.Truncate(" "+row, m.modalContentWidth(), "…")))
		} else {
			b.WriteString(mutedStyle.Render(ansi.Truncate(" "+row, m.modalContentWidth(), "…")))
		}
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", m.modalContentWidth())))
	b.WriteString("\n")

	// Vista previa de las primeras ~6 líneas del bloque seleccionado.
	if block, ok := bm.selectedBlock(); ok {
		preview := strings.Split(block.Content, "\n")
		if len(preview) > 6 {
			preview = append(preview[:6], mutedStyle.Render("…"))
		}
		for _, line := range preview {
			b.WriteString(textStyle.Render("  "))
			b.WriteString(mutedStyle.Render(ansi.Truncate(line, max(4, m.modalContentWidth()-2), "…")))
			b.WriteString("\n")
		}
	}

	if bm.err != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(bm.err))
		b.WriteString("\n")
	}
	if bm.status != "" {
		b.WriteString("\n")
		b.WriteString(successStyle.Render(oneLine(bm.status, m.modalContentWidth())))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("↑/↓ navegar · 1-9 copiar · Enter copiar · Ctrl+S guardar"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Ctrl+R ejecutar (bash/sh) · Esc cerrar"))
	return m.modalBox(b.String())
}

// blockLabel describe un bloque en una línea corta para mensajes de estado.
func blockLabel(block CodeBlock) string {
	return fmt.Sprintf("[%d] %s %s", block.Index, valueOr(block.Lang, "text"), valueOr(block.Path, ""))
}
