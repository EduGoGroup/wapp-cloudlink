// Package server implementa el lado cloud del contrato CloudLink: registra los
// servicios Enrollment y CloudLink y mantiene, por sesión (session_id), el
// stream activo para poder empujar comandos cloud->edge. T2 es transporte vivo
// sin TLS; el mTLS/enrolamiento real llega en T3/T4.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/enroll"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Enroller es la dependencia de enrolamiento del servidor (implementada por
// *enroll.Service). Se inyecta con WithEnroller; si no se inyecta, EnrollEdge
// responde Unimplemented (modo transporte puro de T2/T3).
type Enroller interface {
	Enroll(ctx context.Context, activationCode string, csrPEM []byte) (edgeCertPEM, caChainPEM []byte, tenantID string, err error)
}

// Option configura el Server en New.
type Option func(*Server)

// WithEnroller inyecta el servicio de enrolamiento (T4).
func WithEnroller(e Enroller) Option {
	return func(s *Server) { s.enroller = e }
}

// LeaseIssuer es la dependencia de emisión de leases (implementada por
// *lease.Issuer). Se inyecta con WithLeaseRenewal para habilitar la renovación
// automática del lease al recibir un Heartbeat (T5). Mantiene server desacoplado
// del paquete lease.
type LeaseIssuer interface {
	Issue(edgeID, tenantID string, ttl time.Duration, counter int64) (*cloudlinkv1.LeaseUpdate, error)
}

// WithLeaseRenewal habilita la renovación del lease anclada al Heartbeat: cuando
// llega un Heartbeat con lease_counter=N, el servidor emite un lease renovado con
// counter=N+1 válido por ttl y lo empuja por el mismo stream. Sin esta opción el
// servidor no auto-renueva (la revocación/emisión manual sigue disponible vía
// PushLease).
//
// Granularidad por-Edge (v1): el edgeID se deriva del session_id observado en el
// stream (placeholder; la identidad real del Edge vendrá del cert mTLS en T6).
func WithLeaseRenewal(issuer LeaseIssuer, ttl time.Duration) Option {
	return func(s *Server) {
		s.leaseIssuer = issuer
		s.leaseTTL = ttl
	}
}

// conn envuelve un stream Connect. stream.Send no es seguro para uso
// concurrente, por eso se serializa con sendMu.
type conn struct {
	stream cloudlinkv1.CloudLink_ConnectServer
	sendMu sync.Mutex
}

func (c *conn) send(msg *cloudlinkv1.CloudToEdge) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(msg)
}

// Server implementa EnrollmentServer y CloudLinkServer.
type Server struct {
	cloudlinkv1.UnimplementedEnrollmentServer
	cloudlinkv1.UnimplementedCloudLinkServer

	mu       sync.Mutex
	sessions map[string]*conn

	received chan *cloudlinkv1.EdgeToCloud

	enroller Enroller

	leaseIssuer LeaseIssuer
	leaseTTL    time.Duration
}

// New crea un Server listo para registrar en un *grpc.Server. Sin opciones es el
// transporte puro (T2/T3); con WithEnroller habilita el enrolamiento real (T4).
func New(opts ...Option) *Server {
	s := &Server{
		sessions: make(map[string]*conn),
		received: make(chan *cloudlinkv1.EdgeToCloud, 64),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Received expone los eventos edge->cloud recibidos en cualquier sesión.
func (s *Server) Received() <-chan *cloudlinkv1.EdgeToCloud {
	return s.received
}

// Connect es el handler bidi: drena EdgeToCloud y registra cada session_id
// observado para poder enrutar comandos cloud->edge vía Push.
func (s *Server) Connect(stream cloudlinkv1.CloudLink_ConnectServer) error {
	c := &conn{stream: stream}
	ctx := stream.Context()
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if sid := msg.GetSessionId(); sid != "" {
			s.register(sid, c)
			s.maybeRenewLease(sid, msg, c)
		}
		select {
		case s.received <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// maybeRenewLease emite y empuja un lease renovado cuando llega un Heartbeat y
// hay un LeaseIssuer inyectado. El counter renovado es lease_counter+1
// (monótono; el Validator del Edge rechaza counters viejos). Errores de emisión
// o envío se ignoran a propósito: la renovación es best-effort sobre el stream;
// el lease vigente sigue valiendo hasta su expiración y el Edge reintentará en
// el próximo heartbeat.
func (s *Server) maybeRenewLease(sessionID string, msg *cloudlinkv1.EdgeToCloud, c *conn) {
	if s.leaseIssuer == nil {
		return
	}
	hb := msg.GetHeartbeat()
	if hb == nil {
		return
	}
	lu, err := s.leaseIssuer.Issue(sessionID, "", s.leaseTTL, hb.GetLeaseCounter()+1)
	if err != nil {
		return
	}
	_ = c.send(&cloudlinkv1.CloudToEdge{
		SessionId: sessionID,
		Payload:   &cloudlinkv1.CloudToEdge_LeaseUpdate{LeaseUpdate: lu},
	})
}

// PushLease empuja un LeaseUpdate (renovación o revocación/kill-switch) a una
// sesión registrada. Envoltura sobre Push para el caso lease. La revocación
// anti-clon se dispara llamando PushLease con un LeaseUpdate revocado.
func (s *Server) PushLease(sessionID string, lu *cloudlinkv1.LeaseUpdate) error {
	return s.Push(sessionID, &cloudlinkv1.CloudToEdge{
		SessionId: sessionID,
		Payload:   &cloudlinkv1.CloudToEdge_LeaseUpdate{LeaseUpdate: lu},
	})
}

func (s *Server) register(sessionID string, c *conn) {
	s.mu.Lock()
	s.sessions[sessionID] = c
	s.mu.Unlock()
}

// Push enruta un comando cloud->edge a la sesión indicada. Falla si la sesión
// aún no se ha registrado (el Edge no ha emitido nada con ese session_id).
func (s *Server) Push(sessionID string, cmd *cloudlinkv1.CloudToEdge) error {
	s.mu.Lock()
	c, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("server: sesión desconocida %q", sessionID)
	}
	return c.send(cmd)
}

// EnrollEdge implementa el enrolamiento por código de un solo uso: valida el
// código de activación y el CSR, y devuelve el cert de Edge firmado por la CA del
// tenant. Sobre TLS de servidor (el Edge aún no tiene cert); el cert emitido le
// permite después abrir Connect con mTLS contra la MISMA CA.
//
// Mapeo de errores: código inválido/expirado/usado -> PermissionDenied; CSR
// ausente o inválido -> InvalidArgument. No se filtran secretos en los mensajes.
func (s *Server) EnrollEdge(ctx context.Context, req *cloudlinkv1.EnrollEdgeRequest) (*cloudlinkv1.EnrollEdgeResponse, error) {
	if s.enroller == nil {
		return nil, status.Error(codes.Unimplemented, "EnrollEdge: enrolamiento no configurado en este servidor")
	}
	if len(req.GetCsrPem()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "csr_pem requerido")
	}

	edgeCertPEM, caChainPEM, tenantID, err := s.enroller.Enroll(ctx, req.GetActivationCode(), req.GetCsrPem())
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

	return &cloudlinkv1.EnrollEdgeResponse{
		EdgeCertPem: edgeCertPEM,
		CaChainPem:  caChainPEM,
		TenantId:    tenantID,
	}, nil
}
