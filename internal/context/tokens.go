package context

// EstimateTokens estima tokens con la heurística de ~4 runas por token,
// suficiente para la barra de uso y el disparo de la auto-compactación.
func EstimateTokens(value string) int {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}
