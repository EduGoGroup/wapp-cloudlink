package enroll_test

import (
	"errors"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/internal/enroll"
)

// TestMemoryStoreGC cubre T8/H10: el barrido elimina los códigos expirados sin
// tocar los vigentes, es idempotente y no cambia la semántica (un válido sigue
// consumible; un expirado barrido pasa de ErrCodeExpired a ErrCodeNotFound).
func TestMemoryStoreGC(t *testing.T) {
	s := enroll.NewMemoryStore()
	s.Add("valido", "t", time.Now().Add(time.Hour))
	s.Add("exp1", "t", time.Now().Add(-time.Minute))
	s.Add("exp2", "t", time.Now().Add(-time.Hour))

	if got := s.GC(); got != 2 {
		t.Fatalf("GC eliminó %d, want 2 (los dos expirados)", got)
	}

	// El código vigente sigue consumible.
	if _, err := s.Consume("valido"); err != nil {
		t.Fatalf("válido tras GC: se esperaba consumo OK, got %v", err)
	}
	// Un expirado barrido ya no existe.
	if _, err := s.Consume("exp1"); !errors.Is(err, enroll.ErrCodeNotFound) {
		t.Fatalf("exp1 tras GC: got %v, want ErrCodeNotFound", err)
	}

	// Idempotente: un segundo barrido no elimina nada.
	if got := s.GC(); got != 0 {
		t.Fatalf("GC idempotente: segundo barrido eliminó %d, want 0", got)
	}
}
