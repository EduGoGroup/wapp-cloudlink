package server

import (
	"context"
	"errors"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/enroll"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// enrollmentService implementa EnrollmentServer: el enrolamiento por código de un
// solo uso. Ciclo de vida y superficie de seguridad distintos del transporte vivo
// (por eso vive separado de cloudlinkService, H7).
type enrollmentService struct {
	cloudlinkv1.UnimplementedEnrollmentServer

	enroller Enroller
}

func newEnrollment() *enrollmentService {
	return &enrollmentService{}
}

// EnrollEdge implementa el enrolamiento por código de un solo uso: valida el
// código de activación y el CSR, y devuelve el cert de Edge firmado por la CA del
// tenant. Sobre TLS de servidor (el Edge aún no tiene cert); el cert emitido le
// permite después abrir Connect con mTLS contra la MISMA CA.
//
// Mapeo de errores: código inválido/expirado/usado -> PermissionDenied; CSR
// ausente o inválido -> InvalidArgument. No se filtran secretos en los mensajes.
func (s *enrollmentService) EnrollEdge(ctx context.Context, req *cloudlinkv1.EnrollEdgeRequest) (*cloudlinkv1.EnrollEdgeResponse, error) {
	if s.enroller == nil {
		return nil, status.Error(codes.Unimplemented, "EnrollEdge: enrolamiento no configurado en este servidor")
	}
	if len(req.GetCsrPem()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "csr_pem requerido")
	}

	edgeCertPEM, caChainPEM, tenantID, cloudEncPubkey, err := s.enroller.Enroll(ctx, req.GetActivationCode(), req.GetCsrPem())
	if err != nil {
		switch {
		case errors.Is(err, enroll.ErrInvalidCSR):
			return nil, status.Error(codes.InvalidArgument, "CSR inválido")
		case errors.Is(err, enroll.ErrCodeNotFound),
			errors.Is(err, enroll.ErrCodeExpired),
			errors.Is(err, enroll.ErrCodeUsed):
			return nil, status.Error(codes.PermissionDenied, "código de activación inválido")
		default:
			return nil, status.Error(codes.Internal, "enrolamiento falló")
		}
	}

	// cloud_enc_pubkey: la X25519 pública de la nube para que el Edge selle los
	// sensibles (T6/H8). Se propaga tal cual la entregue el enrolador (puede ir
	// vacía si no está configurada; el contrato ya reservaba el campo).
	return &cloudlinkv1.EnrollEdgeResponse{
		EdgeCertPem:    edgeCertPEM,
		CaChainPem:     caChainPEM,
		TenantId:       tenantID,
		CloudEncPubkey: cloudEncPubkey,
	}, nil
}
