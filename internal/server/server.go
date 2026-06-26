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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
}

// New crea un Server listo para registrar en un *grpc.Server.
func New() *Server {
	return &Server{
		sessions: make(map[string]*conn),
		received: make(chan *cloudlinkv1.EdgeToCloud, 64),
	}
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

// EnrollEdge es un stub: el enrolamiento real (mTLS, PKI) es T3/T4.
func (s *Server) EnrollEdge(_ context.Context, _ *cloudlinkv1.EnrollEdgeRequest) (*cloudlinkv1.EnrollEdgeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "EnrollEdge: enrolamiento real (mTLS/PKI) pendiente en T3/T4")
}
