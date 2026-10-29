package ui

import (
	"testing"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
)

// La compactación automática solo debe dispararse al superar el umbral. Antes
// se lanzaba tras cualquier turno y resumía conversaciones cortas.
func TestAutoCompactRespectsThreshold(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.cfg.CompactThreshold = 1_000_000
	m.ctxMgr.SetMaxTokens(m.cfg.CompactThreshold)
	for i := 0; i < 8; i++ {
		m.history = append(m.history, chatMessage{Role: ai.RoleUser, Content: "mensaje corto"})
	}
	m.refreshUsage()

	if m.shouldAutoCompact() {
		t.Fatal("no debería compactar bajo el umbral")
	}
	next, cmd := m.startAutoCompact()
	if cmd != nil || next.compacting {
		t.Fatal("startAutoCompact lanzó la compactación bajo el umbral")
	}

	m.cfg.CompactThreshold = 1
	m.ctxMgr.SetMaxTokens(1)
	m.refreshUsage()
	if !m.shouldAutoCompact() {
		t.Fatal("debería compactar al superar el umbral")
	}
}
