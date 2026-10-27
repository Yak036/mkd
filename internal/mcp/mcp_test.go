package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Yak036/mkd/internal/config"
)

// TestMain convierte el binario de test en un servidor MCP falso cuando se
// invoca con MKD_FAKE_MCP=1. Así los tests no dependen de npx ni de red.
func TestMain(m *testing.M) {
	if os.Getenv("MKD_FAKE_MCP") == "1" {
		runFakeServer()
		return
	}
	os.Exit(m.Run())
}

// runFakeServer implementa el mínimo protocolo MCP sobre stdio.
func runFakeServer() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		// Las notificaciones (sin id) no llevan respuesta.
		if msg.ID == nil {
			continue
		}

		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{
					"name":        "echo",
					"description": "Devuelve el texto recibido.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required":   []string{"text"},
					},
				},
				map[string]any{
					"name":        "boom",
					"description": "Falla a propósito.",
					"inputSchema": map[string]any{"type": "object"},
				},
			}}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			switch params.Name {
			case "boom":
				result = map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "explosión controlada"}},
					"isError": true,
				}
			default:
				text, _ := params.Arguments["text"].(string)
				result = map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "echo: " + text}},
					"isError": false,
				}
			}
		default:
			writeFakeError(out, msg.ID, codeMethodNotFound, "method not found")
			continue
		}

		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"result":  result,
		})
		fmt.Fprintln(out, string(payload))
		out.Flush()
	}
}

func writeFakeError(out *bufio.Writer, id *json.RawMessage, code int, message string) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	fmt.Fprintln(out, string(payload))
	out.Flush()
}

// toConfig convierte un Spec de test a config.MCPServerConfig.
func (s Spec) toConfig() config.MCPServerConfig {
	return config.MCPServerConfig{Command: s.Command, Args: s.Args, Env: s.Env, Enabled: true}
}

// fakeCommand devuelve un Spec que arranca el servidor falso.
func fakeCommand(t *testing.T, name string) Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{
		Name:    name,
		Command: exe,
		Args:    []string{"-test.run=TestMain"},
		Env:     map[string]string{"MKD_FAKE_MCP": "1"},
	}
}

func TestClientHandshakeListAndCall(t *testing.T) {
	client := NewClient(fakeCommand(t, "fake"))
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer client.Close()

	tools := client.Tools()
	if len(tools) != 2 {
		t.Fatalf("tools=%d, esperaba 2", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Fatalf("primera tool=%q", tools[0].Name)
	}

	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hola"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := result.Text(); got != "echo: hola" {
		t.Fatalf("texto=%q", got)
	}
}

func TestClientCallToolError(t *testing.T) {
	client := NewClient(fakeCommand(t, "fake"))
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer client.Close()

	result, err := client.CallTool(context.Background(), "boom", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("esperaba error isError")
	}
	if !result.IsError || !strings.Contains(result.Text(), "explosión") {
		t.Fatalf("resultado=%#v err=%v", result, err)
	}
}

func TestManagerQualifiedNamesAndRouting(t *testing.T) {
	manager := NewManager(map[string]config.MCPServerConfig{
		"fake": fakeCommand(t, "fake").toConfig(),
	})
	if err := manager.Start(context.Background(), "fake"); err != nil {
		t.Fatalf("manager start: %v", err)
	}
	defer manager.Close()

	tools := manager.Tools()
	if len(tools) != 2 {
		t.Fatalf("tools=%d, esperaba 2", len(tools))
	}
	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Function.Name] = true
	}
	if !names["mcp__fake__echo"] || !names["mcp__fake__boom"] {
		t.Fatalf("nombres prefijados: %#v", names)
	}

	result, err := manager.Call(context.Background(), "mcp__fake__echo", json.RawMessage(`{"text":"ruteado"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := result.Text(); got != "echo: ruteado" {
		t.Fatalf("texto=%q", got)
	}

	statuses := manager.Statuses()
	if len(statuses) != 1 || !statuses[0].Online || len(statuses[0].Tools) != 2 {
		t.Fatalf("statuses=%#v", statuses)
	}
}

func TestQualifiedName(t *testing.T) {
	if got := QualifiedName("filesystem", "read_file"); got != "mcp__filesystem__read_file" {
		t.Fatalf("qualified=%q", got)
	}
	server, tool, ok := ParsePrefixed("mcp__filesystem__read_file")
	if !ok || server != "filesystem" || tool != "read_file" {
		t.Fatalf("parse=%q %q %v", server, tool, ok)
	}
	if _, _, ok := ParsePrefixed("read_file"); ok {
		t.Fatal("read_file no debería ser MCP")
	}
	if _, _, ok := ParsePrefixed("mcp__onlyserver"); ok {
		t.Fatal("falta separador __tool")
	}
}
