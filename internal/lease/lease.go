// Package lease modela el lease operativo de wApp (ADR-0007): el blob firmado
// por el servidor que autoriza al Edge a operar, y su contraparte de validación.
//
// El lease es el kill-switch anti-clon. La fuente de verdad NO son los campos
// sueltos de cloudlinkv1.LeaseUpdate (expires_unix/revoked), sino el payload
// FIRMADO que viaja en LeaseUpdate.lease: el Validator nunca confía en los
// campos top-level sin verificar primero la firma Ed25519.
//
// Roles:
//   - Issuer  (lado servidor): firma y emite LeaseUpdate. Custodia la clave
//     privada Ed25519. Vive en la nube.
//   - Validator (lado Edge, modelado aquí): solo tiene la clave PÚBLICA; verifica
//     firma, expiración y monotonía del counter, y cachea el estado. Aplica el
//     gate 2-de-2 (ADR-0007): CanOperate = hasDEK ∧ leaseVigente. La DEK vive en
//     el Edge; aquí su presencia se modela como un booleano inyectable (T6).
//
// Granularidad v1: POR-EDGE (kill-switch de todo el Edge). El session_id del
// contrato queda reservado para granularidad por-sesión futura; no se usa aquí.
//
// Margen offline v1: el lease vale hasta expires_unix, sin gracia extra. El
// Heartbeat.lease_counter ancla la renovación; el Validator acepta counter
// estrictamente creciente y rechaza uno más viejo (anti-replay básico).
package lease

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
)

// Errores sentinela del paquete.
var (
	// ErrBadSignature: la firma Ed25519 del lease no verifica con la clave
	// pública del Validator (clave equivocada o payload manipulado).
	ErrBadSignature = errors.New("lease: firma inválida")
	// ErrStaleCounter: el lease trae un counter <= al ya cacheado (replay de un
	// lease viejo). No aplica a revocaciones, que siempre son pegajosas.
	ErrStaleCounter = errors.New("lease: counter más viejo que el vigente (replay)")
	// ErrMalformed: el blob del lease no decodifica como un lease firmado.
	ErrMalformed = errors.New("lease: blob malformado")
)

// claims es el contenido firmable del lease. Es la fuente de verdad; los campos
// top-level de LeaseUpdate solo lo espejan para inspección rápida sin verificar.
type claims struct {
	EdgeID      string `json:"edge_id"`
	TenantID    string `json:"tenant_id"`
	ExpiresUnix int64  `json:"expires_unix"`
	Counter     int64  `json:"counter"`
	Revoked     bool   `json:"revoked"`
}

// signedLease es el sobre que viaja en LeaseUpdate.lease: los bytes EXACTOS de
// los claims firmados + la firma sobre esos mismos bytes.
//
// Por qué este encoding (JSON con claims pre-serializados embebidos):
//   - Se firma y se verifica sobre los MISMOS bytes (Claims), que viajan tal
//     cual dentro del sobre. No se re-serializa antes de verificar, así que NO
//     se requiere un encoding canónico/determinista: se evita toda una clase de
//     bugs de "firmé A, verifiqué A' equivalente pero distinto byte a byte".
//   - Stdlib puro (encoding/json + crypto/ed25519): sin dependencias nuevas,
//     greenfield, sin imports a EduGo.
//   - Inspeccionable y round-trippable; []byte se codifica como base64 por json.
//
// Se evitó un sub-mensaje proto a propósito: el .proto no debe tocarse en T5 y
// proto3 no garantiza serialización canónica byte-estable entre versiones, lo
// que complicaría firmar/verificar sobre los mismos bytes.
type signedLease struct {
	Claims []byte `json:"claims"` // JSON de claims; bytes exactos que se firman
	Sig    []byte `json:"sig"`    // ed25519.Sign(priv, Claims)
}

// seal serializa los claims, los firma y devuelve el blob del sobre.
func seal(priv ed25519.PrivateKey, c claims) ([]byte, error) {
	claimsBytes, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("lease: marshal claims: %w", err)
	}
	sig := ed25519.Sign(priv, claimsBytes)
	blob, err := json.Marshal(signedLease{Claims: claimsBytes, Sig: sig})
	if err != nil {
		return nil, fmt.Errorf("lease: marshal sobre: %w", err)
	}
	return blob, nil
}

// open verifica la firma del blob contra la clave pública y devuelve los claims.
// Devuelve ErrBadSignature si la firma no verifica y ErrMalformed si el blob no
// decodifica. No interpreta expiración ni counter: eso es del Validator.
func open(pub ed25519.PublicKey, blob []byte) (claims, error) {
	var sl signedLease
	if err := json.Unmarshal(blob, &sl); err != nil {
		return claims{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if len(sl.Sig) != ed25519.SignatureSize || !ed25519.Verify(pub, sl.Claims, sl.Sig) {
		return claims{}, ErrBadSignature
	}
	var c claims
	if err := json.Unmarshal(sl.Claims, &c); err != nil {
		return claims{}, fmt.Errorf("%w: claims: %v", ErrMalformed, err)
	}
	return c, nil
}
