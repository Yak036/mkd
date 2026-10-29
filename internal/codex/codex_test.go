package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestExtractClaims(t *testing.T) {
	claims := map[string]interface{}{
		"exp": float64(1800000000),
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id": "acc-123456",
		},
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)
	fakeJWT := fmt.Sprintf("eyJhbGciOiJSUzI1NiJ9.%s.signature", payloadB64)

	accountID, exp := ExtractClaims(fakeJWT)
	if accountID != "acc-123456" {
		t.Errorf("esperado accountID 'acc-123456', obtenido %q", accountID)
	}
	if exp != 1800000000 {
		t.Errorf("esperado exp 1800000000, obtenido %d", exp)
	}
}

func TestLocalAuthSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CODEX_HOME", tmpDir)

	original := TokenBundle{
		AccessToken:  "at-test-token",
		RefreshToken: "rt-test-token",
		IDToken:      "it-test-token",
		AccountID:    "acc-999",
		ExpiresAt:    time.Now().Unix() + 3600,
	}

	if err := SaveLocalAuth(original); err != nil {
		t.Fatalf("SaveLocalAuth error: %v", err)
	}

	loaded, err := LoadLocalAuth()
	if err != nil {
		t.Fatalf("LoadLocalAuth error: %v", err)
	}

	if loaded.AccessToken != original.AccessToken {
		t.Errorf("esperado AccessToken %s, obtenido %s", original.AccessToken, loaded.AccessToken)
	}
	if loaded.RefreshToken != original.RefreshToken {
		t.Errorf("esperado RefreshToken %s, obtenido %s", original.RefreshToken, loaded.RefreshToken)
	}
	if loaded.AccountID != original.AccountID {
		t.Errorf("esperado AccountID %s, obtenido %s", original.AccountID, loaded.AccountID)
	}

	// Verificar permisos 0600
	info, err := os.Stat(filepath.Join(tmpDir, "auth.json"))
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("esperado permisos 0600, obtenido %04o", perm)
	}
}

func TestFetchModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("ChatGPT-Account-Id") != "acc-test" {
			http.Error(w, "missing account id", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"models": [
				{"slug": "gpt-5.3-codex", "display_name": "GPT-5.3 Codex"},
				{"slug": "gpt-4o", "display_name": "GPT-4o"}
			]
		}`)
	}))
	defer ts.Close()

	models, err := FetchModels(context.Background(), ts.URL, "test-access-token", "acc-test")
	if err != nil {
		t.Fatalf("FetchModels error: %v", err)
	}

	if len(models) != 2 || models[0] != "gpt-4o" || models[1] != "gpt-5.3-codex" {
		t.Errorf("modelos inesperados: %v", models)
	}
}

func TestStreamResponses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"delta\":\"Hola desde \"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"delta\":\"Codex\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"item\":{\"type\":\"function_call\",\"call_id\":\"call-1\",\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"main.go\\\"}\"}}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "event: response.completed\ndata: {}\n\n")
		flusher.Flush()
	}))
	defer ts.Close()

	req := Request{
		BaseURL:      ts.URL,
		AccessToken:  "test-token",
		Model:        "gpt-5.3-codex",
		Instructions: "Eres un asistente de prueba",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hola"},
		},
		Tools: []openai.Tool{
			{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "read_file",
					Description: "Lee un archivo",
				},
			},
		},
	}

	stream, err := StreamResponses(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamResponses error: %v", err)
	}

	var content string
	var toolCalls []openai.ToolCall
	for ev := range stream {
		if ev.Err != nil {
			t.Fatalf("evento con error: %v", ev.Err)
		}
		if ev.DeltaContent != "" {
			content += ev.DeltaContent
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}

	if content != "Hola desde Codex" {
		t.Errorf("esperado contenido 'Hola desde Codex', obtenido %q", content)
	}
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "read_file" {
		t.Errorf("herramientas inesperadas: %v", toolCalls)
	}
}
