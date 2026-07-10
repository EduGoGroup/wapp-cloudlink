// Package enroll implementa el enrolamiento por código de un solo uso (T4):
// un store de códigos de activación, una CA firmante de CSRs y un cliente de
// enrolamiento que modela lo que hará el Edge en T6 (genera par de claves + CSR,
// pide su certificado y queda listo para conectar por mTLS).
//
// Frontera zero-knowledge: la clave privada del Edge se genera y se queda en el
// cliente; por CloudLink solo viaja el CSR (clave pública + identidad). La CA que
// firma aquí es la MISMA que el servidor mTLS usa en ClientCAs, de modo que un
// Edge recién enrolado completa el handshake mTLS sin pasos extra.
package enroll

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Errores de consumo de código (sentinela). El transporte (server.EnrollEdge)
// los traduce a códigos gRPC; aquí se mantienen agnósticos al transporte.
var (
	// ErrCodeNotFound: el código no existe en el store.
	ErrCodeNotFound = errors.New("enroll: código de activación desconocido")
	// ErrCodeExpired: el código existe pero su TTL ya venció.
	ErrCodeExpired = errors.New("enroll: código de activación expirado")
	// ErrCodeUsed: el código ya fue consumido (es de un solo uso).
	ErrCodeUsed = errors.New("enroll: código de activación ya utilizado")
)

// CodeStore valida y consume códigos de activación de un solo uso.
type CodeStore interface {
	// Consume valida que el código exista, no esté expirado ni usado; al éxito lo
	// marca como usado (atómico) y devuelve el tenant asociado.
	Consume(code string) (tenantID string, err error)
}

type activationCode struct {
	tenantID string
	expiry   time.Time
	used     bool
}

// MemoryStore es una implementación en memoria, segura para concurrencia. Para
// dev/tests se siembran códigos con Add.
type MemoryStore struct {
	mu    sync.Mutex
	codes map[string]*activationCode
	now   func() time.Time
}

// NewMemoryStore crea un store vacío con reloj wall-clock.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		codes: make(map[string]*activationCode),
		now:   time.Now,
	}
}

// Add siembra un código de activación válido hasta expiry para el tenant dado.
// Pensado para dev/tests (en prod los emite la plataforma). Sobrescribe si el
// código ya existía.
func (s *MemoryStore) Add(code, tenantID string, expiry time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = &activationCode{tenantID: tenantID, expiry: expiry}
}

// Consume implementa CodeStore: un solo uso, con validación de existencia, TTL y
// reuso. La transición a "used" ocurre bajo el mismo lock que la validación, de
// modo que dos consumos concurrentes del mismo código no pueden tener éxito ambos.
func (s *MemoryStore) Consume(code string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok {
		return "", ErrCodeNotFound
	}
	if s.now().After(c.expiry) {
		return "", ErrCodeExpired
	}
	if c.used {
		return "", ErrCodeUsed
	}
	c.used = true
	return c.tenantID, nil
}

// GC elimina del store los códigos ya EXPIRADOS (TTL vencido). Los códigos de un
// solo uso son efímeros: sin barrido, los sembrados y caducados se acumularían en
// memoria sin límite. Devuelve cuántos eliminó. Idempotente y seguro para
// concurrencia. Sin regresión de seguridad: un código expirado ya fallaba con
// ErrCodeExpired; tras el GC falla con ErrCodeNotFound (ambos => PermissionDenied
// en el transporte) y jamás puede volver a tener éxito.
func (s *MemoryStore) GC() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	removed := 0
	for code, c := range s.codes {
		if now.After(c.expiry) {
			delete(s.codes, code)
			removed++
		}
	}
	return removed
}

// StartGC lanza un barrido periódico (GC) hasta que ctx se cancele. Conveniencia
// para el arranque de la Plataforma; NewMemoryStore no crea goroutines por sí
// solo, así que el GC periódico es opt-in explícito. every<=0 no arranca nada.
func (s *MemoryStore) StartGC(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.GC()
			}
		}
	}()
}
