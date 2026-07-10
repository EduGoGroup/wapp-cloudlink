package server

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
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

// cloudlinkService implementa CloudLinkServer: el transporte vivo bidi. Mantiene,
// por session_id, el stream activo (para enrutar Push cloud->edge) y drena los
// EdgeToCloud entrantes hacia el consumidor.
type cloudlinkService struct {
	cloudlinkv1.UnimplementedCloudLinkServer

	mu       sync.Mutex
	sessions map[string]*conn

	received chan *cloudlinkv1.EdgeToCloud

	leaseIssuer LeaseIssuer
	leaseTTL    time.Duration
}

func newCloudLink() *cloudlinkService {
	return &cloudlinkService{
		sessions: make(map[string]*conn),
		received: make(chan *cloudlinkv1.EdgeToCloud, 64),
	}
}

// Received expone los eventos edge->cloud recibidos en cualquier sesión.
func (s *cloudlinkService) Received() <-chan *cloudlinkv1.EdgeToCloud {
	return s.received
}

// Connect es el handler bidi: drena EdgeToCloud y registra cada session_id
// observado para poder enrutar comandos cloud->edge vía Push. Al salir el stream
// (EOF, error o corte de contexto) da de baja todas las sesiones que registró
// este conn, evitando enrutar Push/PushLease (incl. el kill-switch anti-clon) a
// un stream zombi (H1). Un Edge multiplexa N sesiones sobre un stream (ADR-0008),
// de ahí que se rastree el conjunto de session_id vistos en este stream.
func (s *cloudlinkService) Connect(stream cloudlinkv1.CloudLink_ConnectServer) error {
	c := &conn{stream: stream}
	ctx := stream.Context()
	mine := make(map[string]struct{})
	defer s.deregister(c, mine)
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
			mine[sid] = struct{}{}
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
func (s *cloudlinkService) maybeRenewLease(sessionID string, msg *cloudlinkv1.EdgeToCloud, c *conn) {
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
func (s *cloudlinkService) PushLease(sessionID string, lu *cloudlinkv1.LeaseUpdate) error {
	return s.Push(sessionID, &cloudlinkv1.CloudToEdge{
		SessionId: sessionID,
		Payload:   &cloudlinkv1.CloudToEdge_LeaseUpdate{LeaseUpdate: lu},
	})
}

func (s *cloudlinkService) register(sessionID string, c *conn) {
	s.mu.Lock()
	s.sessions[sessionID] = c
	s.mu.Unlock()
}

// deregister da de baja las sesiones que registró un conn al terminar su stream.
// Solo borra la entrada si sigue apuntando a ESTE conn: una reconexión más nueva
// pudo haber reemplazado el mapeo (mismo session_id, stream distinto), y en ese
// caso no debe tocarse para no dejar la sesión viva sin ruta (H1).
func (s *cloudlinkService) deregister(c *conn, sessionIDs map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sid := range sessionIDs {
		if s.sessions[sid] == c {
			delete(s.sessions, sid)
		}
	}
}

// Push enruta un comando cloud->edge a la sesión indicada. Falla si la sesión
// aún no se ha registrado (el Edge no ha emitido nada con ese session_id).
func (s *cloudlinkService) Push(sessionID string, cmd *cloudlinkv1.CloudToEdge) error {
	s.mu.Lock()
	c, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("server: sesión desconocida %q", sessionID)
	}
	return c.send(cmd)
}
