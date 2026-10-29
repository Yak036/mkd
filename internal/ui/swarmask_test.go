package ui

import (
	"context"
	"testing"
	"time"

	"github.com/Yak036/mkd/internal/agent"
)

// El puente entrega la pregunta a la TUI y devuelve la respuesta al orquestador.
func TestAskUserBridgeRoundTrip(t *testing.T) {
	bridge := newAskUserBridge()
	done := make(chan askReply, 1)

	go func() {
		answer, cancelled := bridge.Ask(context.Background(), agent.Question{Question: "¿?"})
		done <- askReply{answer: answer, cancelled: cancelled}
	}()

	var request askRequest
	select {
	case request = <-bridge.Requests():
	case <-time.After(time.Second):
		t.Fatal("la pregunta no llegó a la TUI")
	}

	request.reply <- askReply{answer: "Redis"}

	select {
	case reply := <-done:
		if reply.answer != "Redis" || reply.cancelled {
			t.Fatalf("respuesta = %#v", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("el orquestador no recibió la respuesta")
	}
}

// Si la misión se cancela mientras espera, el orquestador no queda bloqueado.
func TestAskUserBridgeCancel(t *testing.T) {
	bridge := newAskUserBridge()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan bool, 1)
	go func() {
		_, cancelled := bridge.Ask(ctx, agent.Question{Question: "¿?"})
		done <- cancelled
	}()

	<-bridge.Requests()
	cancel()

	select {
	case cancelled := <-done:
		if !cancelled {
			t.Fatal("debería reportar cancelación")
		}
	case <-time.After(time.Second):
		t.Fatal("el orquestador quedó bloqueado")
	}
}
