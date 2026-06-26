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
		}
		select {
		case s.received <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
