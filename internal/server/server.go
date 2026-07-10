// Package server implementa el lado cloud del contrato CloudLink: registra los
// servicios Enrollment y CloudLink y mantiene, por sesión (session_id), el
// stream activo para poder empujar comandos cloud->edge.
//
// IMPLEMENTACIÓN DE REFERENCIA / DEMO — no es el servidor de producción.
// El servidor CloudLink real que terminan los Edges vive en la Plataforma Cloud
// (repo wapp-cloud-platform, paquete internal/gateway/grpc): ahí están el mTLS
// estricto, el fleet, la persistencia de leases y los dos listeners. Este
// paquete existe para (a) validar el contrato proto extremo a extremo, (b) los
// arneses e2e de esta pieza (cmd/cloudlink, cmd/democloud) y (c) servir de
// referencia legible del ciclo de vida del stream. Vive bajo internal/ a
// propósito: NO debe importarse desde otros repos; la Plataforma tiene su propia
// implementación y no depende de este paquete (decisión Plan 027 · Ola 0 · T4).
//
// Separación de responsabilidades (Plan 027 · Ola 1 · T7 / H7): el enrolamiento
// (EnrollmentServer, ciclo de vida del cert) y el transporte vivo (CloudLinkServer,
// streams/registro/lease/recepción) tienen superficies de seguridad y ciclos de
// vida distintos, por eso viven en dos tipos separados: enrollmentService
// (enrollment.go) y cloudlinkService (cloudlink.go). Server es solo la FACHADA que
// los compone para poder registrar un único objeto en el *grpc.Server del arnés
// demo; en producción la Plataforma ya los tiene separados.
package server

import (
	"context"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
)

// Enroller es la dependencia de enrolamiento del servidor (implementada por
// *enroll.Service). Se inyecta con WithEnroller; si no se inyecta, EnrollEdge
// responde Unimplemented (modo transporte puro sin PKI).
//
// cloudEncPubkey es la pública X25519 de cifrado de la nube (32B) que el Edge usa
// para sellar los campos sensibles (SealFor) hacia enc_payload (T6/H8). Puede ir
// vacía si el enrolador no la tiene configurada (compat).
type Enroller interface {
	Enroll(ctx context.Context, activationCode string, csrPEM []byte) (edgeCertPEM, caChainPEM []byte, tenantID string, cloudEncPubkey []byte, err error)
}

// LeaseIssuer es la dependencia de emisión de leases (implementada por
// *lease.Issuer). Se inyecta con WithLeaseRenewal para habilitar la renovación
// automática del lease al recibir un Heartbeat. Mantiene el servidor desacoplado
// del paquete lease.
type LeaseIssuer interface {
	Issue(edgeID, tenantID string, ttl time.Duration, counter int64) (*cloudlinkv1.LeaseUpdate, error)
}

// Option configura el Server (fachada) en New, enrutando cada ajuste al servicio
// interno que corresponde.
type Option func(*Server)

// WithEnroller inyecta el servicio de enrolamiento en el enrollmentService.
func WithEnroller(e Enroller) Option {
	return func(s *Server) { s.enrollmentService.enroller = e }
}

// WithInboxCapacity fija el tamaño del buffer acotado de recepción por sesión
// (T5/H2). Al llenarse, deliver descarta el entrante e incrementa el contador de
// saturación de esa sesión sin bloquear la recepción. Valor <=0 usa el default.
func WithInboxCapacity(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.cloudlinkService.inboxCap = n
		}
	}
}

// WithSaturationHook registra un callback que se invoca en cada descarte por
// saturación (T5/H2), con el session_id y el total descartado de esa sesión. Útil
// para loguear/exportar métrica. Debe ser rápido y no bloquear (corre en el path
// de recepción).
func WithSaturationHook(fn func(sessionID string, dropped uint64)) Option {
	return func(s *Server) { s.cloudlinkService.onSaturation = fn }
}

// WithLeaseRenewal habilita la renovación del lease anclada al Heartbeat: cuando
// llega un Heartbeat con lease_counter=N, el servidor emite un lease renovado con
// counter=N+1 válido por ttl y lo empuja por el mismo stream. Sin esta opción el
// servidor no auto-renueva (la revocación/emisión manual sigue disponible vía
// PushLease).
//
// Granularidad por-Edge (v1): el edgeID se deriva del session_id observado en el
// stream (placeholder; la identidad real del Edge vendrá del cert mTLS más
// adelante).
func WithLeaseRenewal(issuer LeaseIssuer, ttl time.Duration) Option {
	return func(s *Server) {
		s.cloudlinkService.leaseIssuer = issuer
		s.cloudlinkService.leaseTTL = ttl
	}
}

// Server es la fachada de referencia/demo que compone los dos servicios gRPC
// (enrolamiento + transporte) en un único objeto registrable. Delega en
// cloudlinkService (Connect/Push/registro/lease/recepción) y enrollmentService
// (EnrollEdge) por promoción de métodos.
type Server struct {
	*cloudlinkService
	*enrollmentService
}

// New crea un Server listo para registrar en un *grpc.Server. Sin opciones es el
// transporte puro (EnrollEdge responde Unimplemented); con WithEnroller habilita
// el enrolamiento real.
func New(opts ...Option) *Server {
	s := &Server{
		cloudlinkService:  newCloudLink(),
		enrollmentService: newEnrollment(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
