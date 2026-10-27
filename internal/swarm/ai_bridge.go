package swarm

// Puente entre el cliente real de IA y la interfaz Completer.
//
//	ai.Client ──(satisfies)──► Completer ◄──(fakeClient en tests)
//
// Mantener esta dependencia aislada deja el orquestador testeable sin red.

import (
	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
)

// compile-time: el cliente real cumple el contrato del sub-agente.
var _ Completer = (*ai.Client)(nil)

// AIClientFactory devuelve una ClientFactory que construye clientes reales,
// heredando provider/model del perfil cuando los define.
func AIClientFactory(cfg config.Config) ClientFactory {
	return func(profile config.SubAgentProfile) (Completer, error) {
		return ai.NewForProfile(cfg, profile.Provider, profile.Model)
	}
}
