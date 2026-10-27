package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mkd/internal/config"
)

// TestFetchCuratedModels: si el proveedor expone /models/list, solo se devuelven
// modelos de texto activos y no privados.
func TestFetchCuratedModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"model_name": "meta/llama-chat", "type": "text-generation", "deprecated": 0},
			{"model_name": "qwen/coder", "type": "text-generation", "deprecated": 0},
			{"model_name": "old/model", "type": "text-generation", "deprecated": 100}, // deprecado
			{"model_name": "img/model", "type": "image-generation", "deprecated": 0},  // no texto
			{"model_name": "private/model", "type": "text-generation", "private": 1},  // privado
		})
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers[cfg.ActiveProvider] = config.ProviderConfig{
		Name: "test", BaseURL: srv.URL + "/v1/openai", APIKey: "k", ActiveModel: "meta/llama-chat",
	}
	cfg.Normalize()
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	models, err := client.FetchAvailableModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("esperaba 2 modelos de texto activos, got %v", models)
	}
	if models[0] != "meta/llama-chat" || models[1] != "qwen/coder" {
		t.Fatalf("models=%v", models)
	}
}

// TestFetchFallbackOpenAI: sin /models/list usa el endpoint OpenAI estándar.
func TestFetchFallbackOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" && r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-x", "object": "model"},
				{"id": "gpt-y", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers[cfg.ActiveProvider] = config.ProviderConfig{
		Name: "test", BaseURL: srv.URL + "/v1", APIKey: "k", ActiveModel: "gpt-x",
	}
	cfg.Normalize()
	client, _ := New(cfg)
	models, err := client.FetchAvailableModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models=%v", models)
	}
}
