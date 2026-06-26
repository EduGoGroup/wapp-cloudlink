package lease

import (
	"crypto/ed25519"
	"errors"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
)

// Issuer emite y revoca leases del lado servidor (la nube). Custodia la clave
// privada Ed25519; el Edge solo tiene la pública. Es seguro para uso
// concurrente: no muta estado interno.
type Issuer struct {
	priv ed25519.PrivateKey
	now  func() time.Time
}

// IssuerOption configura el Issuer.
type IssuerOption func(*Issuer)

// WithIssuerClock inyecta el reloj (tests deterministas). Por defecto time.Now.
func WithIssuerClock(now func() time.Time) IssuerOption {
	return func(i *Issuer) { i.now = now }
}

// NewIssuer crea un Issuer con la clave privada del servidor. Devuelve error si
// la clave no tiene el tamaño Ed25519 esperado.
func NewIssuer(priv ed25519.PrivateKey, opts ...IssuerOption) (*Issuer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("lease: clave privada Ed25519 inválida")
	}
	i := &Issuer{priv: priv, now: time.Now}
	for _, opt := range opts {
		opt(i)
	}
	return i, nil
}

// PublicKey devuelve la clave pública correspondiente (la que recibe el Edge
// para construir su Validator).
func (i *Issuer) PublicKey() ed25519.PublicKey {
	return i.priv.Public().(ed25519.PublicKey)
}

// Issue emite un lease VIGENTE para el Edge indicado, válido por ttl a partir de
// ahora, con el counter dado. Granularidad por-Edge: tenantID es informativo;
// edgeID identifica el Edge gobernado por el kill-switch.
//
// El LeaseUpdate resultante lleva el payload firmado en .lease y ESPEJA
// expires_unix/revoked en los campos top-level (solo para inspección; el
// Validator se rige por el payload firmado).
func (i *Issuer) Issue(edgeID, tenantID string, ttl time.Duration, counter int64) (*cloudlinkv1.LeaseUpdate, error) {
	expires := i.now().Add(ttl).Unix()
	return i.build(claims{
		EdgeID:      edgeID,
		TenantID:    tenantID,
		ExpiresUnix: expires,
		Counter:     counter,
		Revoked:     false,
	})
}

// Revoke emite un LeaseUpdate de REVOCACIÓN (kill-switch) para el Edge. La
// revocación es pegajosa en el Validator: una vez aplicada, ningún lease viejo
// la des-revoca. No depende del counter (un kill-switch debe poder dispararse
// siempre).
func (i *Issuer) Revoke(edgeID, tenantID string) (*cloudlinkv1.LeaseUpdate, error) {
	return i.build(claims{
		EdgeID:      edgeID,
		TenantID:    tenantID,
		ExpiresUnix: i.now().Unix(),
		Counter:     0,
		Revoked:     true,
	})
}

// build firma los claims y arma el LeaseUpdate espejando los campos top-level.
func (i *Issuer) build(c claims) (*cloudlinkv1.LeaseUpdate, error) {
	blob, err := seal(i.priv, c)
	if err != nil {
		return nil, err
	}
	return &cloudlinkv1.LeaseUpdate{
		Lease:       blob,
		ExpiresUnix: c.ExpiresUnix,
		Revoked:     c.Revoked,
	}, nil
}
