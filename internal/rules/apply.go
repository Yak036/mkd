package rules

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Selection describe qué reglas entran en una petición y por qué.
type Selection struct {
	// Rules son las reglas aplicables, ya ordenadas por prioridad.
	Rules []Rule
	// Always son las reglas con alwaysApply (siempre incluidas).
	Always int
	// Matched son las activadas por globs.
	Matched int
	// Manual son las forzadas por `:@rule-nombre`.
	Manual int
}

// Select elige las reglas aplicables a una petición.
//
//   - touched: rutas (relativas al cwd o absolutas) que toca la petición:
//     archivos anclados, menciones y rutas abiertas por el agente.
//   - manual:  nombres de reglas forzadas explícitamente.
//
// Una regla con globs entra si alguno casa con `touched`. Una regla sin globs
// y sin alwaysApply es manual: solo entra si la piden en `manual`.
func Select(list []Rule, touched []string, manual []string) Selection {
	manualSet := make(map[string]bool, len(manual))
	for _, name := range manual {
		manualSet[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, ":")))] = true
	}

	var selection Selection
	chosen := make(map[string]bool, len(list))

	for _, rule := range list {
		name := strings.ToLower(rule.Name)

		switch {
		case rule.AlwaysApply:
			selection.Always++
			addRule(&selection, chosen, rule)
		case manualSet[name]:
			selection.Manual++
			addRule(&selection, chosen, rule)
		case len(rule.Globs) > 0 && matchesAny(rule, touched):
			selection.Matched++
			addRule(&selection, chosen, rule)
		}
	}

	sort.SliceStable(selection.Rules, func(i, j int) bool {
		if selection.Rules[i].Priority != selection.Rules[j].Priority {
			return selection.Rules[i].Priority > selection.Rules[j].Priority
		}
		return selection.Rules[i].Path < selection.Rules[j].Path
	})
	return selection
}

func addRule(selection *Selection, chosen map[string]bool, rule Rule) {
	if chosen[rule.Path] {
		return
	}
	chosen[rule.Path] = true
	selection.Rules = append(selection.Rules, rule)
}

// matchesAny comprueba si algún patrón de la regla casa con alguna ruta.
func matchesAny(rule Rule, touched []string) bool {
	for _, pattern := range rule.Globs {
		for _, target := range touched {
			if matchGlob(rule.Scope, pattern, target) {
				return true
			}
		}
	}
	return false
}

// matchGlob resuelve patrones tipo `internal/**/*.go`, `*.md` o `cmd/*.go`
// contra una ruta. Los patrones relativos se resuelven contra el Scope de la
// regla; los que empiezan por `/` o contienen un directorio se anclan al
// Scope, y los que solo tienen el nombre de archivo casan en cualquier nivel.
func matchGlob(scope, pattern, target string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}

	rel := normalizeTarget(scope, target)
	rel = path.Clean(filepath.ToSlash(rel))
	pattern = path.Clean(filepath.ToSlash(pattern))

	// `**/*.go` → casa con cualquier ruta que acabe en .go.
	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		return globMatch(suffix, rel) || globMatch(suffix, path.Base(rel))
	}

	// Patrón sin separador: se permite en cualquier nivel (como .gitignore).
	if !strings.Contains(pattern, "/") {
		return globMatch(pattern, path.Base(rel)) || globMatch(pattern, rel)
	}

	return globMatch(pattern, rel)
}

// normalizeTarget convierte la ruta a relativa al scope de la regla.
func normalizeTarget(scope, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if filepath.IsAbs(target) {
		if scope != "" {
			if rel, err := filepath.Rel(scope, target); err == nil && !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
		return target
	}
	// Ruta relativa al cwd: si el scope es un ancestro, quitamos el prefijo.
	if scope != "" && scope != "." {
		if rel, err := filepath.Rel(scope, mustAbs(target)); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return target
}

// globMatch implementa `*`, `?` y `**` sobre segmentos ya normalizados.
func globMatch(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			// `**` consume cero o más segmentos.
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if !matchSegment(pattern[0], name[0]) {
			return false
		}
		pattern = pattern[1:]
		name = name[1:]
	}
	return len(name) == 0
}

// matchSegment implementa `*` y `?` dentro de un segmento sin `/`.
func matchSegment(pattern, name string) bool {
	pi, ni := 0, 0
	star, mark := -1, 0

	for ni < len(name) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == name[ni]):
			pi++
			ni++
		case pi < len(pattern) && pattern[pi] == '*':
			star = pi
			mark = ni
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ni = mark
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
