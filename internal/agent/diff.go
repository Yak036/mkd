package agent

import (
	"strings"
)

// DiffKind clasifica una línea del diff unificado.
type DiffKind int

const (
	DiffContext DiffKind = iota // línea sin cambios (prefijo " ")
	DiffAdd                     // línea añadida (prefijo "+")
	DiffRemove                  // línea eliminada (prefijo "-")
)

// String devuelve el prefijo textual de la línea según su tipo.
func (k DiffKind) String() string {
	switch k {
	case DiffAdd:
		return "+"
	case DiffRemove:
		return "-"
	default:
		return " "
	}
}

// DiffLine es una línea del diff con sus números de línea original y nuevo.
// OldNo es 0 para líneas añadidas; NewNo es 0 para líneas eliminadas.
type DiffLine struct {
	Kind  DiffKind
	OldNo int
	NewNo int
	Text  string
}

// diffCell es una operación elemental del algoritmo LCS.
type diffCell struct {
	kind DiffKind
	old  int // índice 0-based en oldLines
	new  int // índice 0-based en newLines
}

// maxLCSLines es el umbral de seguridad del LCS por programación dinámica.
// Por encima se degrada a un diff trivial (todo remove + todo add) para no
// explotar en memoria ni tiempo. 4000² celdas ≈ 16 M operaciones.
const maxLCSLines = 4000

// UnifiedDiff calcula un diff unificado entre oldContent y newContent con
// 'context' líneas de contexto alrededor de cada cambio.
//
// Algoritmo:
//  1. Dividir ambos textos en líneas (sin el '\n' final vacío).
//  2. LCS por programación dinámica: lcs[i][j] = longitud de la subsecuencia
//     común más larga de old[i:] y new[j:]. Se recorre en orden inverso para
//     poder reconstruir hacia adelante.
//  3. Backtracking desde (0,0): si old[i]==new[j] → contexto; si
//     lcs[i+1][j] >= lcs[i][j+1] → eliminación; si no → adición. El desempate
//     favorece la eliminación para que el diff sea estable y legible.
//  4. Agrupar los cambios en hunks y conservar solo 'context' líneas a cada
//     lado de un cambio; los tramos idénticos se colapsan.
//  5. Numerar las líneas resultantes: OldNo/NewNo reflejan el documento real.
//
// Si alguna secuencia supera maxLCSLines se degrada a "todo remove + todo add"
// (forzando contexto=0) para mantener la TUI fluida.
func UnifiedDiff(oldContent, newContent, path string, context int) []DiffLine {
	if context < 0 {
		context = 0
	}
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	var ops []diffCell
	if len(oldLines) > maxLCSLines || len(newLines) > maxLCSLines {
		ops = trivialDiff(len(oldLines), len(newLines))
		context = 0
	} else {
		ops = lcsDiff(oldLines, newLines)
	}

	// Marcar qué operaciones se muestran: todo cambio + ventanas de contexto.
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == DiffContext {
			continue
		}
		start := max(0, i-context)
		end := min(len(ops), i+context+1)
		for k := start; k < end; k++ {
			keep[k] = true
		}
	}

	lines := make([]DiffLine, 0, len(ops))
	for i, op := range ops {
		if !keep[i] {
			continue
		}
		line := DiffLine{Kind: op.kind}
		switch op.kind {
		case DiffContext:
			line.OldNo = op.old + 1
			line.NewNo = op.new + 1
			line.Text = oldLines[op.old]
		case DiffRemove:
			line.OldNo = op.old + 1
			line.Text = oldLines[op.old]
		case DiffAdd:
			line.NewNo = op.new + 1
			line.Text = newLines[op.new]
		}
		lines = append(lines, line)
	}
	return lines
}

// CountDiff suma líneas añadidas y eliminadas de un diff.
func CountDiff(lines []DiffLine) (adds, removes int) {
	for _, line := range lines {
		switch line.Kind {
		case DiffAdd:
			adds++
		case DiffRemove:
			removes++
		}
	}
	return adds, removes
}

// lcsDiff construye la secuencia de operaciones con LCS por DP.
func lcsDiff(oldLines, newLines []string) []diffCell {
	n, m := len(oldLines), len(newLines)
	// tabla (n+1)x(m+1); fila i y columna j miran hacia delante.
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	ops := make([]diffCell, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			ops = append(ops, diffCell{kind: DiffContext, old: i, new: j})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffCell{kind: DiffRemove, old: i})
			i++
		default:
			ops = append(ops, diffCell{kind: DiffAdd, new: j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffCell{kind: DiffRemove, old: i})
	}
	for ; j < m; j++ {
		ops = append(ops, diffCell{kind: DiffAdd, new: j})
	}
	return ops
}

// trivialDiff produce "todas las viejas eliminadas, todas las nuevas añadidas".
func trivialDiff(oldCount, newCount int) []diffCell {
	ops := make([]diffCell, 0, oldCount+newCount)
	for i := 0; i < oldCount; i++ {
		ops = append(ops, diffCell{kind: DiffRemove, old: i})
	}
	for j := 0; j < newCount; j++ {
		ops = append(ops, diffCell{kind: DiffAdd, new: j})
	}
	return ops
}

// splitLines divide el contenido en líneas sin el salto final.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
