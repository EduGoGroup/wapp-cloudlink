package lease

import (
	"crypto/ed25519"
	"sync"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
)

// Validator modela el puerto de validación del lado Edge (su cableado real en el
// daemon es T6). Solo tiene la clave PÚBLICA: verifica firma, expiración y
// monotonía del counter, y cachea el estado vigente/expirado/revocado.
//
// Aplica el gate 2-de-2 (ADR-0007): CanOperate = hasDEK ∧ leaseVigente. La DEK
// vive en el Edge; aquí su presencia es un booleano inyectado en CanOperate.
//
// Seguro para uso concurrente.
type Validator struct {
	pub ed25519.PublicKey
	now func() time.Time

	mu          sync.Mutex
	applied     bool  // ¿se aplicó alguna vez un lease vigente?
	revoked     bool  // pegajoso: una vez true, no vuelve a false
	expiresUnix int64 // del último lease vigente aplicado
	counter     int64 // último counter aceptado (para anti-replay)
}

// ValidatorOption configura el Validator.
type ValidatorOption func(*Validator)

// WithValidatorClock inyecta el reloj (tests deterministas). Por defecto time.Now.
func WithValidatorClock(now func() time.Time) ValidatorOption {
	return func(v *Validator) { v.now = now }
}

// NewValidator crea un Validator con la clave pública del servidor.
func NewValidator(pub ed25519.PublicKey, opts ...ValidatorOption) *Validator {
	v := &Validator{pub: pub, now: time.Now}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Apply verifica e incorpora un LeaseUpdate. Comportamiento:
//   - Verifica la firma sobre el payload de .lease. Si no verifica (clave
//     equivocada o manipulación), devuelve error y NO cambia el estado: un lease
//     no firmado jamás vuelve el estado a "vigente".
//   - Revocación: si el payload trae revoked=true, marca el estado como revocado
//     de forma PEGAJOSA, sin importar el counter (kill-switch siempre aplica).
//   - Renovación: si el estado ya está revocado, un lease vigente NO lo
//     des-revoca (se ignora la renovación, sin error). Si no, exige counter
//     estrictamente creciente (anti-replay) y cachea expiración + counter.
//
// La fuente de verdad es el payload firmado, no los campos top-level del
// LeaseUpdate.
func (v *Validator) Apply(lu *cloudlinkv1.LeaseUpdate) error {
	if lu == nil {
		return ErrMalformed
	}
	c, err := open(v.pub, lu.GetLease())
	if err != nil {
		return err // firma inválida o malformado: estado intacto
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if c.Revoked {
		v.revoked = true // pegajoso
		return nil
	}

	if v.revoked {
		// Kill-switch ya disparado: ningún lease (ni válido) re-habilita en v1.
		return nil
	}

	if v.applied && c.Counter <= v.counter {
		return ErrStaleCounter // estado intacto
	}

	v.applied = true
	v.expiresUnix = c.ExpiresUnix
	v.counter = c.Counter
	return nil
}

// CanOperate aplica el gate 2-de-2: el Edge puede operar solo si tiene la DEK
// (hasDEK, inyectado) Y el lease está vigente (firma válida ya aplicada, no
// revocado y no expirado). hasDEK=false bloquea aunque el lease sea vigente.
func (v *Validator) CanOperate(hasDEK bool) bool {
	return hasDEK && v.leaseVigente()
}

func (v *Validator) leaseVigente() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.applied || v.revoked {
		return false
	}
	return v.now().Unix() < v.expiresUnix
}

// Revoked indica si el kill-switch se disparó (estado pegajoso). Útil para el
// Edge/diagnóstico.
func (v *Validator) Revoked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.revoked
}
