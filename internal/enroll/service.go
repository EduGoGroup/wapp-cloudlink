package enroll

import "context"

// Service orquesta el enrolamiento del lado servidor: valida el CSR, consume el
// código de un solo uso y firma con la CA. Es agnóstico al transporte; el mapeo a
// códigos gRPC vive en internal/server (EnrollEdge).
type Service struct {
	codes CodeStore
	ca    *CA
	// cloudEncPub: pública X25519 (32B) de cifrado de la nube que el Edge recibe
	// para sellar los sensibles (T6/H8). Vacía si no se configuró.
	cloudEncPub []byte
}

// ServiceOption configura opcionalmente el Service en NewService.
type ServiceOption func(*Service)

// WithCloudEncPubkey inyecta la pública X25519 (32B) de cifrado de la nube que se
// devolverá en EnrollEdgeResponse.cloud_enc_pubkey para que el Edge selle los
// campos sensibles (T6/H8).
func WithCloudEncPubkey(pub []byte) ServiceOption {
	return func(s *Service) { s.cloudEncPub = pub }
}

// NewService cablea el store de códigos con la CA firmante. La CA inyectada debe
// ser la misma cuyo Pool() alimenta mtls.ServerCreds(ClientCAs).
func NewService(codes CodeStore, ca *CA, opts ...ServiceOption) *Service {
	s := &Service{codes: codes, ca: ca}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CA expone la CA firmante (para construir el endpoint mTLS con la misma CA).
func (s *Service) CA() *CA { return s.ca }

// Enroll valida y firma. Orden deliberado: primero verifica el CSR (si es
// inválido NO se quema el código), luego consume el código de un solo uso y por
// último emite el cert. Devuelve los campos de EnrollEdgeResponse (incluida la
// cloud_enc_pubkey configurada, T6/H8) o un error sentinela (ErrInvalidCSR /
// ErrCode*).
func (s *Service) Enroll(_ context.Context, activationCode string, csrPEM []byte) (edgeCertPEM, caChainPEM []byte, tenantID string, cloudEncPubkey []byte, err error) {
	if _, err = ParseAndVerifyCSR(csrPEM); err != nil {
		return nil, nil, "", nil, err
	}
	tenantID, err = s.codes.Consume(activationCode)
	if err != nil {
		return nil, nil, "", nil, err
	}
	edgeCertPEM, caChainPEM, err = s.ca.SignCSR(csrPEM, tenantID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	return edgeCertPEM, caChainPEM, tenantID, s.cloudEncPub, nil
}
