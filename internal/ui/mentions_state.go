package ui

// MentionsState agrupa el estado de las menciones @ (fuzzy finder) para que
// el workspace lo embeba como un único campo: `mentions MentionsState`.
//
//	Ciclo de vida:
//	  Update: m.updateMentions() tras cada tecla del textarea
//	          ↑/↓ → m.moveMentionSelection(±1) · Tab → m.completeMention()
//	          Enter (popup abierto) → m.applySelectedMention()
//	  View:   base = overlayAt(base, m.mentionsView(), 2, y, w, h)
//	  Envío:  m.mentionsContextBlock() se inyecta antes del historial
//
// Los métodos viven en mentions.go.
type MentionsState struct {
	popup mentionPopup

	// Mentions guarda el contenido resuelto de las menciones especiales que el
	// token de texto ya no puede reconstruir (@git:diff, @tree…). El bloque se
	// inyecta en orden al enviar con mentionsContextBlock().
	Mentions map[string]string

	// index cachea el FileIndex por directorio de trabajo.
	index *mentionIndexCache
}
