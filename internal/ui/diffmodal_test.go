package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mkd/internal/agent"
)

func TestDecisionForKey(t *testing.T) {
	dm := diffModal{visible: true}
	cases := []struct {
		key  string
		want approvalDecision
		ok   bool
	}{
		{"y", ApprovalApply, true},
		{"Y", ApprovalApply, true},
		{"enter", ApprovalApply, true},
		{"n", ApprovalReject, true},
		{"N", ApprovalReject, true},
		{"esc", ApprovalReject, true},
		{"a", ApprovalAlways, true},
		{"A", ApprovalAlways, true},
		{"x", ApprovalReject, false},
		{"tab", ApprovalReject, false},
	}
	for _, tc := range cases {
		got, ok := dm.decisionForKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		if tc.key == "enter" {
			got, ok = dm.decisionForKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
		if tc.key == "esc" {
			got, ok = dm.decisionForKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
		if tc.key == "tab" {
			got, ok = dm.decisionForKey(tea.KeyMsg{Type: tea.KeyTab})
		}
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("decisionForKey(%q) = (%v,%v), want (%v,%v)", tc.key, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRenderDiffLine(t *testing.T) {
	add := renderDiffLine(agent.DiffLine{Kind: agent.DiffAdd, NewNo: 7, Text: "nueva"}, 60)
	if !strings.Contains(add, "+ nueva") {
		t.Fatalf("add sin prefijo: %q", add)
	}
	remove := renderDiffLine(agent.DiffLine{Kind: agent.DiffRemove, OldNo: 3, Text: "vieja"}, 60)
	if !strings.Contains(remove, "- vieja") {
		t.Fatalf("remove sin prefijo: %q", remove)
	}
	ctx := renderDiffLine(agent.DiffLine{Kind: agent.DiffContext, OldNo: 9, NewNo: 9, Text: "igual"}, 60)
	if !strings.Contains(ctx, "   igual") {
		t.Fatalf("contexto sin prefijo: %q", ctx)
	}
}

func TestDiffModalTarget(t *testing.T) {
	dm := diffModal{proposal: agent.Proposal{Path: "a.go", Adds: 2, Removes: 1}}
	if got := dm.target(); !strings.Contains(got, "a.go (+2 / -1") {
		t.Fatalf("target archivo = %q", got)
	}
	dm.proposal = agent.Proposal{Action: agent.ActionRun, Command: "go test ./..."}
	if got := dm.target(); !strings.Contains(got, "Comando: go test") {
		t.Fatalf("target comando = %q", got)
	}
}
