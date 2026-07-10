package server

import (
	"testing"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
)

// TestDeliverPerSessionBackpressure cubre T5/H2 de forma determinista (white-box,
// sin fan-in ni goroutines): con buffer acotado por sesión, deliver descarta el
// sobrante de una sesión saturada e incrementa su métrica, SIN afectar a otra
// sesión (aislamiento del head-of-line).
func TestDeliverPerSessionBackpressure(t *testing.T) {
	var satCalls int
	s := New(
		WithInboxCapacity(2),
		WithSaturationHook(func(string, uint64) { satCalls++ }),
	)
	cl := s.cloudlinkService
	msg := func(sid string) *cloudlinkv1.EdgeToCloud { return &cloudlinkv1.EdgeToCloud{SessionId: sid} }

	// Sin consumidor de Received() no hay forwarder: el inbox de A se llena a
	// cap=2 y los 3 restantes se descartan (drop-newest).
	for i := 0; i < 5; i++ {
		cl.deliver("A", msg("A"))
	}
	if got := cl.Dropped("A"); got != 3 {
		t.Fatalf("A: descartes esperados 3 (5 entregados, cap 2), got %d", got)
	}

	// Aislamiento: B con exactamente cap mensajes NO descarta nada, pese a que A
	// esté saturada (su backlog no consume el buffer de B).
	for i := 0; i < 2; i++ {
		cl.deliver("B", msg("B"))
	}
	if got := cl.Dropped("B"); got != 0 {
		t.Fatalf("B: no debía descartar (aislada de A), got %d", got)
	}

	if got := cl.TotalDropped(); got != 3 {
		t.Fatalf("total de descartes: got %d, want 3", got)
	}
	if satCalls != 3 {
		t.Fatalf("hook de saturación: got %d llamadas, want 3", satCalls)
	}
}
