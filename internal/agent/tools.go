package agent

import (
	"encoding/json"

	openai "github.com/sashabaranov/go-openai"
)

// Nombres canónicos de las herramientas.
const (
	ToolReadFile   = "read_file"
	ToolListDir    = "list_dir"
	ToolSearchCode = "search_code"
	ToolWriteFile  = "write_file"
	ToolPatchFile  = "patch_file"
	ToolRunCommand = "run_command"
)

// AskUserTool es la herramienta de preguntas interactivas. Se mantiene aparte
// de Tools porque no modifica disco ni ejecuta procesos y no se auto-aprueba:
// su única función es devolverle una decisión al agente.
var AskUserTool = functionTool(ToolAskUser,
	"Pregunta algo al usuario y espera su respuesta antes de continuar. Úsala cuando "+
		"falte una preferencia o decisión que cambie el resultado (alcance, enfoque, "+
		"librería, estilo) y no puedas deducirla del contexto. Devuelve la opción elegida "+
		"o el texto libre. No la uses para pedir permiso de escritura o ejecución: eso ya "+
		"tiene su propio flujo de aprobación.",
	schema(map[string]any{
		"question": prop("string", "Pregunta concreta y breve dirigida al usuario."),
		"header":   prop("string", "Etiqueta corta (máximo ~24 caracteres) para el modal."),
		"options": map[string]any{
			"type":        "array",
			"description": "Alternativas entre las que elegir. Máximo 6. Puede ir vacío para forzar texto libre.",
			"maxItems":    MaxQuestionOptions,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"label":       prop("string", "Texto corto de la opción; es lo que se devuelve."),
					"description": prop("string", "Cuándo elegir esta opción (opcional)."),
				},
				"required": []string{"label"},
			},
		},
		"allow_custom": prop("boolean", "Permite además escribir una respuesta libre (por defecto true si no hay opciones)."),
	}, "question"))

// Tools es el catálogo que se envía al modelo en cada petición
// (openai.ChatCompletionRequest.Tools). Las descripciones son parte del
// contrato: el modelo elige la herramienta por nombre y descripción.
var Tools = []openai.Tool{
	functionTool(ToolReadFile,
		"Lee un archivo de texto del proyecto y lo devuelve con números de línea. "+
			"Truncado a 256 KiB. Usa start_line/end_line (1-indexado) para trozos grandes.",
		schema(map[string]any{
			"path":       prop("string", "Ruta relativa al directorio de trabajo."),
			"start_line": prop("integer", "Primera línea a incluir (1-indexado, opcional)."),
			"end_line":   prop("integer", "Última línea a incluir, inclusive (opcional)."),
		}, "path")),
	functionTool(ToolListDir,
		"Lista el contenido de un directorio del proyecto. Los directorios terminan en /. "+
			"Oculta .git y devuelve nombre y tamaño de cada entrada.",
		schema(map[string]any{
			"path": prop("string", "Directorio relativo al cwd. Vacío o '.' = raíz del proyecto."),
		})),
	functionTool(ToolSearchCode,
		"Busca un patrón de texto/regex en el código del proyecto. Devuelve líneas con "+
			"formato ruta:línea: texto. Usa ripgrep si está disponible.",
		schema(map[string]any{
			"query":       prop("string", "Texto o expresión regular a buscar."),
			"path":        prop("string", "Ruta a buscar (archivo o directorio). Por defecto todo el cwd."),
			"max_results": prop("integer", "Máximo de coincidencias (por defecto 200)."),
		}, "query")),
	functionTool(ToolWriteFile,
		"Crea o reemplaza por completo un archivo del proyecto. Requiere aprobación del usuario: "+
			"se le muestra un diff unificado antes de aplicar.",
		schema(map[string]any{
			"path":    prop("string", "Ruta relativa del archivo a escribir."),
			"content": prop("string", "Contenido completo final del archivo."),
		}, "path", "content")),
	functionTool(ToolPatchFile,
		"Reemplaza un fragmento exacto de un archivo existente. 'search' debe aparecer una sola vez. "+
			"Requiere aprobación del usuario y muestra el diff resultante.",
		schema(map[string]any{
			"path":    prop("string", "Ruta relativa del archivo a modificar."),
			"search":  prop("string", "Fragmento exacto (incluye indentación) a reemplazar."),
			"replace": prop("string", "Nuevo fragmento que sustituye a 'search'."),
		}, "path", "search", "replace")),
	functionTool(ToolRunCommand,
		"Ejecuta un comando de shell en el directorio del proyecto y devuelve exit code + salida "+
			"(stdout + stderr). Requiere aprobación del usuario. Timeout por defecto 120 s (máx. 600).",
		schema(map[string]any{
			"command":         prop("string", "Comando completo para el shell del sistema ("+shellHint()+")."),
			"timeout_seconds": prop("integer", "Timeout en segundos (por defecto 120, máximo 600)."),
		}, "command")),
}

// FunctionTool construye una tool de tipo function con parámetros ya
// serializados. Se exporta para que la capa MCP reutilice el mismo formato.
func FunctionTool(name, description string, parameters json.RawMessage) openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

// functionTool construye una tool de tipo function con sus parámetros.
func functionTool(name, description string, parameters map[string]any) openai.Tool {
	raw, err := json.Marshal(parameters)
	if err != nil {
		// Los esquemas son literales: un error aquí es un bug de programación,
		// no un fallo recuperable en runtime.
		panic("agent: esquema inválido para " + name + ": " + err.Error())
	}
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(raw),
		},
	}
}

// schema arma un objeto JSON Schema con propiedades y campos requeridos.
func schema(properties map[string]any, required ...string) map[string]any {
	out := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// prop describe una propiedad JSON Schema simple.
func prop(kind, description string) map[string]any {
	return map[string]any{
		"type":        kind,
		"description": description,
	}
}
