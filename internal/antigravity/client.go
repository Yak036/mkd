// Package antigravity implementa el transporte y cliente para invocar modelos a través de la CLI agy de Google Antigravity.
package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// DefaultModel es el modelo rápido y de alto razonamiento por defecto de Antigravity.
const DefaultModel = "gemini-3.8-flash-high"

// Request encapsula los parámetros de una petición de conversación a Antigravity.
type Request struct {
	Model        string
	SystemPrompt string
	Messages     []openai.ChatCompletionMessage
}

// StreamEvent representa un fragmento de texto generado en tiempo real o un error.
type StreamEvent struct {
	DeltaContent string
	Err          error
}

// ndjsonStepUpdate representa la estructura de un evento step_update de agy.
type ndjsonStepUpdate struct {
	Event      string `json:"event"`
	StepUpdate *struct {
		State     string `json:"state"`
		StepType  string `json:"step_type"`
		TextDelta string `json:"text_delta"`
	} `json:"step_update"`
	Result *struct {
		Status   string `json:"status"`
		Response string `json:"response"`
	} `json:"result"`
	Error string `json:"error"`
}

// FormatPrompt concatena instrucciones de sistema y el historial de mensajes en un prompt unificado para agy.
func FormatPrompt(systemPrompt string, messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder

	// Directriz prioritaria para que el proceso agy no intente ejecutar herramientas headless de su propio runtime
	sb.WriteString("[Directriz de ejecución: Estás integrado como motor de inferencia dentro de MKD CLI. El entorno de herramientas, archivos y servidores MCP está gestionado y orquestado enteramente por MKD. En este subproceso CLI no ejecutes herramientas del host por tu cuenta; responde directamente al diálogo siguiendo las instrucciones provistas por MKD.]\n\n")

	if strings.TrimSpace(systemPrompt) != "" {
		sb.WriteString("[Instrucciones del sistema:\n")
		sb.WriteString(strings.TrimSpace(systemPrompt))
		sb.WriteString("\n]\n\n")
	}

	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		switch msg.Role {
		case openai.ChatMessageRoleSystem:
			sb.WriteString("[Regla del sistema: ")
			sb.WriteString(content)
			sb.WriteString("]\n\n")
		case openai.ChatMessageRoleUser:
			sb.WriteString("Usuario: ")
			sb.WriteString(content)
			sb.WriteString("\n\n")
		case openai.ChatMessageRoleAssistant:
			sb.WriteString("Asistente: ")
			sb.WriteString(content)
			sb.WriteString("\n\n")
		case openai.ChatMessageRoleTool:
			sb.WriteString("[Resultado de herramienta: ")
			sb.WriteString(content)
			sb.WriteString("]\n\n")
		}
	}

	res := strings.TrimSpace(sb.String())
	if res == "" {
		return "Hola"
	}
	return res
}

// FetchModels consulta los modelos habilitados en la cuenta de Antigravity ejecutando `agy models`.
func FetchModels(ctx context.Context) ([]string, error) {
	agyPath, err := FindAgyBinary()
	if err == nil {
		cmdCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()

		cmd := exec.CommandContext(cmdCtx, agyPath, "models")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			var models []string
			seen := make(map[string]bool)
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "Fetching") || strings.HasPrefix(line, "⠋") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) > 0 {
					modelID := fields[0]
					// Validar que parece un nombre de modelo válido
					if !strings.Contains(modelID, "...") && !seen[modelID] {
						seen[modelID] = true
						models = append(models, modelID)
					}
				}
			}
			if len(models) > 0 {
				sort.Strings(models)
				return models, nil
			}
		}
	}

	// Catálogo garantizado de Antigravity si no se pudo consultar agy dinámicamente
	models := []string{
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-low",
		"gemini-4-argon-high",
		"gemini-4-argon-medium",
		"gemini-4-argon-low",
		"claude-sonnet-5-5-high",
		"claude-sonnet-5-5-medium",
		"claude-sonnet-5-5-low",
		"claude-opus-5-5-high",
		"claude-opus-5-5-medium",
		"claude-opus-5-5-low",
		"gpt-oss-120b-medium",
	}
	sort.Strings(models)
	return models, nil
}

// StreamMessages lanza la ejecución de `agy` en modo stream-json y emite deltas de texto incremental.
func StreamMessages(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	agyPath, err := EnsureAgyReady()
	if err != nil {
		return nil, err
	}

	model := req.Model
	if model == "" {
		model = DefaultModel
	}

	prompt := FormatPrompt(req.SystemPrompt, req.Messages)

	// Invocamos agy con formato stream-json y desactivando slash commands para evitar interferencias
	cmd := exec.CommandContext(
		ctx,
		agyPath,
		"--disable-slash-commands",
		"--dangerously-skip-permissions",
		"--model", model,
		"--output-format", "stream-json",
		"-p", prompt,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("error conectando salida de agy: %w", err)
	}

	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("error al iniciar agy: %w", err)
	}

	events := make(chan StreamEvent, 64)

	go func() {
		defer close(events)
		defer stdout.Close()
		if stderr != nil {
			defer stderr.Close()
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		var totalEmitted int

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				events <- StreamEvent{Err: ctx.Err()}
				_ = cmd.Process.Kill()
				return
			default:
			}

			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}

			var update ndjsonStepUpdate
			if err := json.Unmarshal([]byte(line), &update); err != nil {
				continue
			}

			if update.Error != "" {
				events <- StreamEvent{Err: errors.New(update.Error)}
				return
			}

			if update.StepUpdate != nil && update.StepUpdate.TextDelta != "" {
				events <- StreamEvent{DeltaContent: update.StepUpdate.TextDelta}
				totalEmitted += len(update.StepUpdate.TextDelta)
			}

			// Fallback: si agy devolvió el resultado final completo en result.response y no emitió deltas
			if update.Result != nil && strings.TrimSpace(update.Result.Response) != "" && totalEmitted == 0 {
				events <- StreamEvent{DeltaContent: update.Result.Response}
				totalEmitted += len(update.Result.Response)
			}
		}

		if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
			events <- StreamEvent{Err: err}
			return
		}

		_ = cmd.Wait()

		// Si el proceso terminó sin emitir texto ni error, notificamos error descriptivo en lugar de dejar el chat en blanco
		if totalEmitted == 0 && ctx.Err() == nil {
			events <- StreamEvent{Err: errors.New("Antigravity terminó sin generar texto de respuesta")}
		}
	}()

	return events, nil
}
