package context

import "mkd/internal/ai"

// compactSplit calcula cuántos mensajes antiguos entran en el 70% inicial,
// dejando siempre los últimos CompactKeepRecent intactos.
//
//	total = 10, keep = 4  →  target = 7  →  split = 6 (10-4)
//	total = 5,  keep = 4  →  target = 3  →  split = 1
//	total = 4,  keep = 4  →  split = 0 (nada que compactar)
func compactSplit(total int) int {
	if total <= CompactKeepRecent {
		return 0
	}
	target := int(float64(total) * compactRatio)
	if target < 1 {
		target = 1
	}
	split := target
	if maxSplit := total - CompactKeepRecent; split > maxSplit {
		split = maxSplit
	}
	if split < 1 {
		return 0
	}
	return split
}

// historyTokens estima los tokens de un historial de mensajes.
func historyTokens(history []ai.Message) int {
	total := 0
	for _, msg := range history {
		total += EstimateTokens(msg.Content) + 4
	}
	return total
}
