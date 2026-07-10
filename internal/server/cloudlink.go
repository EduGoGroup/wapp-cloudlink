package server

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
)

// defaultInboxCapacity es el tamaño del buffer acotado por sesión (T5/H2).
const defaultInboxCapacity = 64

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

// inbox es el buffer acotado de recepción de UNA sesión (T5/H2). dropped cuenta
// los mensajes descartados por saturación de esa sesión.
type inbox struct {
	ch      chan *cloudlinkv1.EdgeToCloud
	dropped atomic.Uint64
}

// cloudlinkService implementa CloudLinkServer: el transporte vivo bidi. Mantiene,
// por session_id, el stream activo (para enrutar Push cloud->edge) y un inbox
// acotado por sesión (para drenar los EdgeToCloud entrantes con backpressure
// aislado, T5/H2).
type cloudlinkService struct {
	cloudlinkv1.UnimplementedCloudLinkServer

	mu       sync.Mutex
	sessions map[string]*conn

	// inboxes: un buffer acotado por sesión. Aísla la recepción entre Edges: la
	// saturación de una sesión no consume el buffer de otra ni bloquea su Recv.
	inboxes  map[string]*inbox
	inboxCap int
	// out + fanin: canal común perezoso que materializa Received() volcando cada
	// inbox aislado a un único canal (conveniencia del arnés demo; ver Received).
	out          chan *cloudlinkv1.EdgeToCloud
	fanin        bool
	onSaturation func(sessionID string, dropped uint64)

	leaseIssuer LeaseIssuer
	leaseTTL    time.Duration
}

func newCloudLink() *cloudlinkService {
	return &cloudlinkService{
		sessions: make(map[string]*conn),
		inboxes:  make(map[string]*inbox),
		inboxCap: defaultInboxCapacity,
	}
}

// deliver encola un EdgeToCloud en el inbox de su sesión con backpressure
// EXPLÍCITO y AISLADO por sesión: buffer acotado (inboxCap) y, al llenarse,
// DESCARTA el mensaje entrante (drop-newest) e incrementa el contador de
// saturación de esa sesión; NUNCA bloquea el goroutine de Recv. Así un consumidor
// lento o una sesión ruidosa pierde solo SUS mensajes y jamás congela la
// recepción de los demás Edges (elimina el head-of-line global, H2). El precio
// —perder mensajes bajo saturación sostenida— es aceptable: la durabilidad la da
// el outbox del Edge (ADR-0003), que reintenta al reconectar.
func (s *cloudlinkService) deliver(sessionID string, msg *cloudlinkv1.EdgeToCloud) {
	s.mu.Lock()
	ib := s.inboxes[sessionID]
	if ib == nil {
		ib = &inbox{ch: make(chan *cloudlinkv1.EdgeToCloud, s.inboxCap)}
		s.inboxes[sessionID] = ib
		if s.fanin {
			go s.forward(ib)
		}
	}
	hook := s.onSaturation
	s.mu.Unlock()

	select {
	case ib.ch <- msg:
	default:
		n := ib.dropped.Add(1)
		if hook != nil {
			hook(sessionID, n)
		}
	}
}

// Received expone un canal ÚNICO con los eventos edge->cloud de TODAS las
// sesiones, como conveniencia para el arnés demo y los tests. Se materializa de
// forma perezosa: en la primera llamada arranca un forwarder por sesión que
// vuelca su inbox aislado a este canal común. NOTA: este canal común es un cuello
// único (un solo consumidor) y NO ofrece el aislamiento por-sesión que sí
// garantiza deliver en la INGESTA; la Plataforma real consume por sesión.
func (s *cloudlinkService) Received() <-chan *cloudlinkv1.EdgeToCloud {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil {
		s.out = make(chan *cloudlinkv1.EdgeToCloud, s.inboxCap)
		s.fanin = true
		for _, ib := range s.inboxes {
			go s.forward(ib)
		}
	}
	return s.out
}

// forward vuelca un inbox de sesión al canal común de Received(). Si el consumidor
// es lento, se bloquea SOLO este forwarder; su inbox se llena y deliver descarta
// para ESA sesión, sin afectar la recepción de las demás.
func (s *cloudlinkService) forward(ib *inbox) {
	for msg := range ib.ch {
		s.out <- msg
	}
}

// Dropped devuelve cuántos EdgeToCloud se descartaron por saturación en una
// sesión (métrica de backpressure, T5/H2). 0 si la sesión no existe.
func (s *cloudlinkService) Dropped(sessionID string) uint64 {
	s.mu.Lock()
	ib := s.inboxes[sessionID]
	s.mu.Unlock()
	if ib == nil {
		return 0
	}
	return ib.dropped.Load()
}

// TotalDropped suma los descartes por saturación de todas las sesiones.
func (s *cloudlinkService) TotalDropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total uint64
	for _, ib := range s.inboxes {
		total += ib.dropped.Load()
	}
	return total
}

// Connect es el handler bidi: drena EdgeToCloud y registra cada session_id
// observado para poder enrutar comandos cloud->edge vía Push. Al salir el stream
// (EOF, error o corte de contexto) da de baja todas las sesiones que registró
// este conn, evitando enrutar Push/PushLease (incl. el kill-switch anti-clon) a
// un stream zombi (H1). Un Edge multiplexa N sesiones sobre un stream (ADR-0008),
// de ahí que se rastree el conjunto de session_id vistos en este stream.
func (s *cloudlinkService) Connect(stream cloudlinkv1.CloudLink_ConnectServer) error {
	c := &conn{stream: stream}
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
		sid := msg.GetSessionId()
		if sid != "" {
			s.register(sid, c)
			mine[sid] = struct{}{}
			s.maybeRenewLease(sid, msg, c)
		}
		// deliver es no-bloqueante (backpressure por sesión): el Recv de esta
		// sesión nunca se congela por un consumidor lento u otra sesión (H2).
		s.deliver(sid, msg)
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
