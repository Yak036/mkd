package config

import (
	"testing"
)

func TestDefaultsContainsClaude(t *testing.T) {
	cfg := Defaults()
	claude, ok := cfg.Providers["claude"]
	if !ok {
		t.Fatalf("se esperaba que Defaults contuviera el proveedor 'claude'")
	}
	if claude.BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("BaseURL inesperada: %s", claude.BaseURL)
	}
	if claude.ActiveModel == "" {
		t.Errorf("ActiveModel no debe estar vacío")
	}
	if len(claude.Models) == 0 {
		t.Errorf("Models no debe estar vacío")
	}
}

func TestIsClaude(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://api.anthropic.com/v1", true},
		{"https://api.anthropic.com", true},
		{"claude", true},
		{"https://api.openai.com/v1", false},
		{"https://api.deepinfra.com/v1/openai", false},
	}

	for _, tc := range cases {
		got := IsClaude(tc.url)
		if got != tc.want {
			t.Errorf("IsClaude(%q) = %v; want %v", tc.url, got, tc.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := DisplayName("claude"); got != "Claude (Anthropic)" {
		t.Errorf("DisplayName('claude') = %q, want 'Claude (Anthropic)'", got)
	}
}
