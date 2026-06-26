package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/client"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// newTransport monta servidor + cliente sobre bufconn (in-memory, sin red ni
// TLS) y devuelve ambos extremos listos.
func newTransport(t *testing.T) (*server.Server, grpc.ClientConnInterface) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := server.New()
	gs := grpc.NewServer()
	cloudlinkv1.RegisterCloudLinkServer(gs, srv)
	cloudlinkv1.RegisterEnrollmentServer(gs, srv)

	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	cc, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	return srv, cc
}

// TestBidiCorrelation verifica el transporte vivo en ambos sentidos y la
// correlación por command_id/session_id, de forma determinista (canales +
// contexto con timeout, sin sleeps).
func TestBidiCorrelation(t *testing.T) {
	srv, cc := newTransport(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	const (
		sessionID = "sess-1"
		upID      = "cmd-up-1"
		downID    = "cmd-down-1"
	)

	// --- edge -> cloud ---
	// El cliente envía un IncomingMessage; esto además registra la sesión en el
	// servidor para que el push cloud->edge posterior tenga destino.
	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		CommandId: upID,
		SessionId: sessionID,
		Payload: &cloudlinkv1.EdgeToCloud_Incoming{
			Incoming: &cloudlinkv1.IncomingMessage{From: "5491100000000", Text: "hola"},
		},
	}); err != nil {
		t.Fatalf("cli.Send: %v", err)
	}

	select {
	case got := <-srv.Received():
		if got.GetCommandId() != upID {
			t.Errorf("edge->cloud command_id: got %q, want %q", got.GetCommandId(), upID)
		}
		if got.GetSessionId() != sessionID {
			t.Errorf("edge->cloud session_id: got %q, want %q", got.GetSessionId(), sessionID)
		}
		if got.GetIncoming().GetText() != "hola" {
			t.Errorf("edge->cloud payload: got %q, want %q", got.GetIncoming().GetText(), "hola")
		}
	case <-ctx.Done():
		t.Fatalf("edge->cloud: timeout esperando en el servidor: %v", ctx.Err())
	}

	// --- cloud -> edge ---
	// El servidor empuja un SendText a la sesión registrada.
	if err := srv.Push(sessionID, &cloudlinkv1.CloudToEdge{
		CommandId: downID,
		SessionId: sessionID,
		Payload: &cloudlinkv1.CloudToEdge_SendText{
			SendText: &cloudlinkv1.SendText{To: "5491100000000", Text: "respuesta"},
		},
	}); err != nil {
		t.Fatalf("srv.Push: %v", err)
	}

	select {
	case got := <-cli.Received():
		if got.GetCommandId() != downID {
			t.Errorf("cloud->edge command_id: got %q, want %q", got.GetCommandId(), downID)
		}
		if got.GetSessionId() != sessionID {
			t.Errorf("cloud->edge session_id: got %q, want %q", got.GetSessionId(), sessionID)
		}
		if got.GetSendText().GetText() != "respuesta" {
			t.Errorf("cloud->edge payload: got %q, want %q", got.GetSendText().GetText(), "respuesta")
		}
	case <-ctx.Done():
		t.Fatalf("cloud->edge: timeout esperando en el cliente: %v", ctx.Err())
	}
}

// TestPushUnknownSession verifica que enrutar a una sesión no registrada falla.
func TestPushUnknownSession(t *testing.T) {
	srv := server.New()
	if err := srv.Push("no-existe", &cloudlinkv1.CloudToEdge{}); err == nil {
		t.Fatal("Push a sesión desconocida: se esperaba error, got nil")
	}
}
