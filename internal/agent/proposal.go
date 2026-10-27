package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Action identifica la operación solicitada por el modelo.
type Action int

const (
	ActionRead Action = iota
	ActionList
	ActionSearch
	ActionWrite
	ActionPatch
	ActionRun
)

// String devuelve el nombre lógico de la acción.
func (a Action) String() string {
	switch a {
	case ActionRead:
		return "read"
	case ActionList:
		return "list"
	case ActionSearch:
		return "search"
	case ActionWrite:
		return "write"
	case ActionPatch:
		return "patch"
	case ActionRun:
		return "run"
	default:
		return "unknown"
	}
}

// RequiresApproval indica si la acción modifica disco o ejecuta procesos.
func (a Action) RequiresApproval() bool {
	switch a {
	case ActionWrite, ActionPatch, ActionRun:
		return true
	default:
		return false
	}
}

// Proposal es la representación previa a ejecutar una herramienta. La UI la
// usa para el diff modal y para decidir si pide permiso.
type Proposal struct {
	Action       Action
	Tool         string
	Path         string
	Diff         []DiffLine
	Adds         int
	Removes      int
	Command      string
	AutoApproved bool
	Summary      string

	// Campos internos para Execute: no se muestran en la UI.
	call        Call
	content     string // write: contenido final
	search      string // patch: fragmento a buscar
	replace     string // patch: fragmento de reemplazo
	oldContent  string // write/patch: contenido original
	timeoutSecs int    // run: timeout efectivo
}

// ReadLimit es el máximo de bytes que read_file devuelve al modelo.
const ReadLimit = 256 << 10

// buildContext es el número de líneas de contexto usadas en los diffs.
const buildContext = 3

// BuildProposal valida la llamada, resuelve rutas dentro del cwd, calcula el
// diff (si aplica) y decide si la acción se auto-aprueba.
func BuildProposal(call Call, cwd string) (Proposal, error) {
	switch call.Name {
	case ToolReadFile:
		return buildRead(call, cwd)
	case ToolListDir:
		return buildList(call, cwd)
	case ToolSearchCode:
		return buildSearch(call, cwd)
	case ToolWriteFile:
		return buildWrite(call, cwd)
	case ToolPatchFile:
		return buildPatch(call, cwd)
	case ToolRunCommand:
		return buildRun(call, cwd)
	default:
		return Proposal{}, fmt.Errorf("herramienta desconocida: %q", call.Name)
	}
}

type readArgs struct {
	Path      string `json:"path"`
	StartLine *int   `json:"start_line"`
	EndLine   *int   `json:"end_line"`
}

func buildRead(call Call, cwd string) (Proposal, error) {
	var args readArgs
	if err := DecodeArguments(call, &args); err != nil {
		return Proposal{}, err
	}
	abs, rel, err := SafePath(cwd, cleanRel(args.Path))
	if err != nil {
		return Proposal{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Proposal{}, fmt.Errorf("no se pudo abrir %s: %w", rel, err)
	}
	if info.IsDir() {
		return Proposal{}, fmt.Errorf("%s es un directorio; usa list_dir", rel)
	}
	summary := "leer " + rel
	if args.StartLine != nil || args.EndLine != nil {
		summary += fmt.Sprintf(" (líneas %d-%d)",
			intOr(args.StartLine, 1), intOr(args.EndLine, 0))
	}
	return Proposal{
		Action:       ActionRead,
		Tool:         call.Name,
		Path:         rel,
		AutoApproved: true,
		Summary:      summary,
		call:         call,
	}, nil
}

type listArgs struct {
	Path string `json:"path"`
}

func buildList(call Call, cwd string) (Proposal, error) {
	var args listArgs
	if err := DecodeArguments(call, &args); err != nil {
		return Proposal{}, err
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	abs, rel, err := SafePath(cwd, cleanRel(args.Path))
	if err != nil {
		return Proposal{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Proposal{}, fmt.Errorf("no se pudo listar %s: %w", rel, err)
	}
	if !info.IsDir() {
		return Proposal{}, fmt.Errorf("%s no es un directorio", rel)
	}
	return Proposal{
		Action:       ActionList,
		Tool:         call.Name,
		Path:         rel,
		AutoApproved: true,
		Summary:      "listar " + rel,
		call:         call,
	}, nil
}

type searchArgs struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	MaxResults *int   `json:"max_results"`
}

func buildSearch(call Call, cwd string) (Proposal, error) {
	var args searchArgs
	if err := DecodeArguments(call, &args); err != nil {
		return Proposal{}, err
	}
	if strings.TrimSpace(args.Query) == "" {
		return Proposal{}, errors.New("search_code requiere 'query'")
	}
	scope := strings.TrimSpace(args.Path)
	rel := "."
	if scope != "" {
		_, resolved, err := SafePath(cwd, cleanRel(scope))
		rel = resolved
		if err != nil {
			return Proposal{}, err
		}
	}
	return Proposal{
		Action:       ActionSearch,
		Tool:         call.Name,
		Path:         rel,
		AutoApproved: true,
		Summary:      fmt.Sprintf("buscar %q en %s", args.Query, rel),
		call:         call,
	}, nil
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func buildWrite(call Call, cwd string) (Proposal, error) {
	var args writeArgs
	if err := DecodeArguments(call, &args); err != nil {
		return Proposal{}, err
	}
	abs, rel, err := SafePath(cwd, cleanRel(args.Path))
	if err != nil {
		return Proposal{}, err
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		return Proposal{}, fmt.Errorf("%s es un directorio", rel)
	}

	old, _ := os.ReadFile(abs) // err => archivo nuevo
	diff := UnifiedDiff(string(old), args.Content, rel, buildContext)
	adds, removes := CountDiff(diff)

	return Proposal{
		Action:      ActionWrite,
		Tool:        call.Name,
		Path:        rel,
		Diff:        diff,
		Adds:        adds,
		Removes:     removes,
		Summary:     fmt.Sprintf("escribir %s (+%d / -%d)", rel, adds, removes),
		call:        call,
		content:     args.Content,
		oldContent:  string(old),
		timeoutSecs: 0,
	}, nil
}

type patchArgs struct {
	Path    string `json:"path"`
	Search  string `json:"search"`
	Replace string `json:"replace"`
}

func buildPatch(call Call, cwd string) (Proposal, error) {
	var args patchArgs
	if err := DecodeArguments(call, &args); err != nil {
		return Proposal{}, err
	}
	if args.Search == "" {
		return Proposal{}, errors.New("patch_file requiere 'search' no vacío")
	}
	abs, rel, err := SafePath(cwd, cleanRel(args.Path))
	if err != nil {
		return Proposal{}, err
	}
	old, err := os.ReadFile(abs)
	if err != nil {
		return Proposal{}, fmt.Errorf("no se pudo leer %s: %w", rel, err)
	}

	updated, err := applyPatch(string(old), args.Search, args.Replace)
	if err != nil {
		return Proposal{}, fmt.Errorf("%s: %w", rel, err)
	}
	diff := UnifiedDiff(string(old), updated, rel, buildContext)
	adds, removes := CountDiff(diff)

	return Proposal{
		Action:     ActionPatch,
		Tool:       call.Name,
		Path:       rel,
		Diff:       diff,
		Adds:       adds,
		Removes:    removes,
		Summary:    fmt.Sprintf("parchear %s (+%d / -%d)", rel, adds, removes),
		call:       call,
		search:     args.Search,
		replace:    args.Replace,
		oldContent: string(old),
	}, nil
}

type runArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
}

func buildRun(call Call, cwd string) (Proposal, error) {
	var args runArgs
	if err := DecodeArguments(call, &args); err != nil {
		return Proposal{}, err
	}
	if strings.TrimSpace(args.Command) == "" {
		return Proposal{}, errors.New("run_command requiere 'command'")
	}
	return Proposal{
		Action:      ActionRun,
		Tool:        call.Name,
		Command:     args.Command,
		Summary:     "ejecutar: " + oneLineCommand(args.Command),
		call:        call,
		timeoutSecs: clampTimeout(args.TimeoutSeconds),
	}, nil
}

// applyPatch exige una única coincidencia exacta de search en content.
func applyPatch(content, search, replace string) (string, error) {
	count := strings.Count(content, search)
	switch {
	case count == 0:
		return "", errors.New("'search' no aparece en el archivo; no se modificó nada")
	case count > 1:
		return "", fmt.Errorf("'search' aparece %d veces; incluye más contexto para que sea único; no se modificó nada", count)
	}
	return strings.Replace(content, search, replace, 1), nil
}

// SafePath resuelve p contra cwd y garantiza que el resultado quede dentro
// del árbol del proyecto. Rechaza escapes por '..' y rutas absolutas externas.
func SafePath(cwd, p string) (abs string, rel string, err error) {
	if strings.TrimSpace(cwd) == "" {
		return "", "", errors.New("directorio de trabajo vacío")
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", "", fmt.Errorf("cwd inválido: %w", err)
	}
	root = filepath.Clean(root)

	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	// Una ruta absoluta solo es válida si ya apunta dentro del proyecto; si no,
	// se rechaza en vez de reinterpretarla (evita sorpresas con /etc/passwd).
	if filepath.IsAbs(p) {
		target := filepath.Clean(p)
		if !withinRoot(root, target) {
			return "", "", fmt.Errorf("ruta fuera del proyecto: %q", p)
		}
		rel, err := filepath.Rel(root, target)
		if err != nil {
			return "", "", err
		}
		return target, filepath.ToSlash(rel), nil
	}

	target := filepath.Clean(filepath.Join(root, p))
	if !withinRoot(root, target) {
		return "", "", fmt.Errorf("ruta fuera del proyecto: %q", p)
	}
	rel, err = filepath.Rel(root, target)
	if err != nil {
		return "", "", err
	}
	return target, filepath.ToSlash(rel), nil
}

// withinRoot comprueba containment con separador para no confundir
// /proj y /proj-otro.
func withinRoot(root, target string) bool {
	if target == root {
		return true
	}
	return strings.HasPrefix(target, root+string(filepath.Separator))
}

// clampTimeout aplica el rango [1, 600] con 120 s por defecto.
func clampTimeout(value *int) int {
	if value == nil || *value <= 0 {
		return 120
	}
	if *value > 600 {
		return 600
	}
	if *value < 1 {
		return 1
	}
	return *value
}

func intOr(ptr *int, fallback int) int {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

// cleanRel normaliza rutas relativas que llegan del modelo: separadores
// Windows, puntos dobles internos y "./".
func cleanRel(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = filepath.Clean(p)
	if p == "." {
		return "."
	}
	return p
}

// oneLineCommand recorta un comando muy largo para las vistas compactas.
func oneLineCommand(command string) string {
	command = strings.ReplaceAll(command, "\n", " ")
	command = strings.ReplaceAll(command, "\r", "")
	if len(command) > 80 {
		return command[:79] + "…"
	}
	return command
}
