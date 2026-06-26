// Command democloud es un driver de NUBE DE DEMOSTRACIÓN para el e2e local/real de
// CloudLink (Plan 004, follow-up). NO es la Plataforma Cloud (Pieza 03): es un arnés
// de desarrollo que (a) levanta el servidor CloudLink insecure, (b) loguea cada
// EdgeToCloud que llega del Edge (verás el IncomingMessage real reenviado por el Edge),
// y (c) lee comandos por stdin para empujar órdenes cloud->edge (SendText), de modo que
// el Edge las despache por WhatsApp real.
//
// Insecure + sin lease a propósito (mTLS/enrolamiento/lease ya están probados en
// T3/T4/T5): este driver enfoca el FLUJO de negocio extremo a extremo.
//
// Uso:
//
//	go run ./cmd/democloud                 # escucha en :8443 (CLOUDLINK_ADDR para cambiar)
//	# en stdin, una vez el Edge conectó (verás "sesión registrada"):
//	send <sessionID> <to> <texto...>       # empuja un SendText a esa sesión
//	ping <sessionID>                       # empuja un Ping
//	quit
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"google.golang.org/grpc"
)

func main() {
	log.SetFlags(log.LstdFlags)

	addr := envOr("CLOUDLINK_ADDR", ":8443")
	srv := server.New()

	gs := grpc.NewServer() // insecure: driver de demo
	cloudlinkv1.RegisterCloudLinkServer(gs, srv)
	cloudlinkv1.RegisterEnrollmentServer(gs, srv)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("democloud: escuchar en %s: %v", addr, err)
	}

	go func() {
		log.Printf("democloud: servidor CloudLink (INSECURE) escuchando en %s", addr)
		if err := gs.Serve(lis); err != nil {
			log.Fatalf("democloud: serve: %v", err)
		}
	}()

	// (b) Logger de eventos edge->cloud: muestra lo que el Edge reenvía.
	go logIncoming(srv)

	// (c) Lector de comandos cloud->edge. Por defecto stdin; si CLOUDLINK_CMD_FILE está
	// seteado, hace tail-poll de ese archivo (modo dirigible en background).
	fmt.Println("democloud listo. Comandos: 'send <sessionID> <to> <texto...>' | 'ping <sessionID>' | 'quit'")
	if cmdFile := os.Getenv("CLOUDLINK_CMD_FILE"); cmdFile != "" {
		log.Printf("democloud: leyendo comandos de %q (tail-poll)", cmdFile)
		tailCommands(srv, cmdFile)
		return
	}
	readCommands(srv, bufio.NewScanner(os.Stdin))
}

// tailCommands hace polling del archivo de comandos: ejecuta cada línea NUEVA
// apendida (ignora el contenido previo posicionándose al final al arrancar).
func tailCommands(srv *server.Server, path string) {
	// Crea el archivo si no existe para poder seguirlo desde cero.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		log.Fatalf("democloud: abrir cmd-file %q: %v", path, err)
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		log.Fatalf("democloud: seek cmd-file: %v", err)
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil { // EOF: nada nuevo, espera y reintenta
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if quit := execCommand(srv, strings.TrimSpace(line)); quit {
			return
		}
	}
}

// seenSessions registra qué sesiones ya anunciamos (primer mensaje observado).
var (
	seen   = map[string]bool{}
	seenMu sync.Mutex
	cmdSeq atomic.Int64
)

func logIncoming(srv *server.Server) {
	for msg := range srv.Received() {
		sid := msg.GetSessionId()
		announceSession(sid)
		switch p := msg.GetPayload().(type) {
		case *cloudlinkv1.EdgeToCloud_Incoming:
			in := p.Incoming
			log.Printf("⬅️  ENTRANTE  session=%s from=%s is_group=%v ts=%d wa_id=%s\n            texto: %q",
				sid, in.GetFrom(), in.GetIsGroup(), in.GetTsUnix(), in.GetWaMessageId(), in.GetText())
		case *cloudlinkv1.EdgeToCloud_Ack:
			a := p.Ack
			log.Printf("⬅️  ACK       session=%s acked=%s ok=%v err=%q", sid, a.GetAckedCommandId(), a.GetOk(), a.GetError())
		case *cloudlinkv1.EdgeToCloud_Delivery:
			d := p.Delivery
			log.Printf("⬅️  DELIVERY  session=%s wa_id=%s status=%s", sid, d.GetWaMessageId(), d.GetStatus())
		case *cloudlinkv1.EdgeToCloud_Heartbeat:
			log.Printf("⬅️  HEARTBEAT session=%s counter=%d", sid, p.Heartbeat.GetLeaseCounter())
		case *cloudlinkv1.EdgeToCloud_Pong:
			log.Printf("⬅️  PONG      session=%s nonce=%d", sid, p.Pong.GetNonce())
		default:
			log.Printf("⬅️  (otro)    session=%s payload=%T", sid, msg.GetPayload())
		}
	}
}

func announceSession(sid string) {
	if sid == "" {
		return
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if !seen[sid] {
		seen[sid] = true
		log.Printf("✅ sesión registrada: %q (ya puedes 'send %s <to> <texto>')", sid, sid)
	}
}

func readCommands(srv *server.Server, sc *bufio.Scanner) {
	for sc.Scan() {
		if quit := execCommand(srv, strings.TrimSpace(sc.Text())); quit {
			return
		}
	}
}

// execCommand ejecuta una línea de comando. Devuelve true si pide salir.
func execCommand(srv *server.Server, line string) bool {
	if line == "" {
		return false
	}
	fields := strings.Fields(line)
	switch fields[0] {
	case "quit", "exit":
		log.Println("democloud: saliendo.")
		return true
	case "send":
		// send <sessionID> <to> <texto...>
		if len(fields) < 4 {
			log.Println("uso: send <sessionID> <to> <texto...>")
			return false
		}
		sid, to := fields[1], fields[2]
		text := strings.Join(fields[3:], " ")
		cmdID := nextCmdID("send")
		err := srv.Push(sid, &cloudlinkv1.CloudToEdge{
			CommandId: cmdID,
			SessionId: sid,
			Payload:   &cloudlinkv1.CloudToEdge_SendText{SendText: &cloudlinkv1.SendText{To: to, Text: text}},
		})
		if err != nil {
			log.Printf("➡️  SEND falló (¿sesión conectada?): %v", err)
			return false
		}
		log.Printf("➡️  SEND enviado command_id=%s session=%s to=%s texto=%q", cmdID, sid, to, text)
	case "ping":
		if len(fields) < 2 {
			log.Println("uso: ping <sessionID>")
			return false
		}
		sid := fields[1]
		if err := srv.Push(sid, &cloudlinkv1.CloudToEdge{
			CommandId: nextCmdID("ping"),
			SessionId: sid,
			Payload:   &cloudlinkv1.CloudToEdge_Ping{Ping: &cloudlinkv1.Ping{Nonce: cmdSeq.Load()}},
		}); err != nil {
			log.Printf("➡️  PING falló: %v", err)
			return false
		}
		log.Printf("➡️  PING enviado session=%s", sid)
	default:
		log.Printf("comando desconocido %q (usa: send | ping | quit)", fields[0])
	}
	return false
}

func nextCmdID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, cmdSeq.Add(1))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
