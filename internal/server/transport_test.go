package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/client"
	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
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

// newServer monta un servidor sobre bufconn y devuelve el servidor más un
// dialer para abrir tantas conexiones de cliente como haga falta (necesario para
// simular desconexión + reconexión con streams distintos).
func newServer(t *testing.T) (*server.Server, func(ctx context.Context) grpc.ClientConnInterface) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := server.New()
	gs := grpc.NewServer()
	cloudlinkv1.RegisterCloudLinkServer(gs, srv)
	cloudlinkv1.RegisterEnrollmentServer(gs, srv)

	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dial := func(ctx context.Context) grpc.ClientConnInterface {
		cc, err := grpc.NewClient(
			"passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("grpc.NewClient: %v", err)
		}
		t.Cleanup(func() { _ = cc.Close() })
		return cc
	}
	return srv, dial
}

// registerSession abre un stream, envía un Incoming con sessionID (lo que registra
// la sesión en el servidor) y drena srv.Received() para garantizar que el
// servidor ya procesó el registro antes de continuar.
func registerSession(t *testing.T, srv *server.Server, cc grpc.ClientConnInterface, ctx context.Context, sessionID string) *client.Client {
	t.Helper()
	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		SessionId: sessionID,
		Payload: &cloudlinkv1.EdgeToCloud_Incoming{
			Incoming: &cloudlinkv1.IncomingMessage{From: "x", Text: "reg"},
		},
	}); err != nil {
		t.Fatalf("cli.Send(registro): %v", err)
	}
	select {
	case <-srv.Received():
	case <-ctx.Done():
		t.Fatalf("registro: timeout esperando en el servidor: %v", ctx.Err())
	}
	return cli
}

// TestDeregisterOnDisconnectAndReconnect cubre H1/T1: al caer el stream la sesión
// se da de baja (Push deja de encontrarla) y una reconexión con el MISMO
// session_id recupera la ruta, sin quedar bloqueada por la sesión fantasma ni
// que la baja tardía del stream viejo borre el registro nuevo.
func TestDeregisterOnDisconnectAndReconnect(t *testing.T) {
	srv, dial := newServer(t)

	const sid = "sess-reconnect"

	// --- Conexión A: registra y confirma que Push tiene ruta. ---
	ctxA, cancelA := context.WithCancel(context.Background())
	registerSession(t, srv, dial(ctxA), ctxA, sid)
	if err := srv.Push(sid, &cloudlinkv1.CloudToEdge{SessionId: sid}); err != nil {
		t.Fatalf("Push con A conectada: se esperaba éxito, got %v", err)
	}

	// --- Cae A: la baja debe ocurrir y dejar la sesión sin ruta. ---
	cancelA()
	deadline := time.After(3 * time.Second)
	for {
		if err := srv.Push(sid, &cloudlinkv1.CloudToEdge{SessionId: sid}); err != nil {
			break // sesión dada de baja
		}
		select {
		case <-deadline:
			t.Fatal("H1: la sesión no se dio de baja tras la desconexión (Push sigue encontrándola)")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// --- Reconexión B con el mismo session_id: recupera la ruta y recibe el push. ---
	ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelB()
	cliB := registerSession(t, srv, dial(ctxB), ctxB, sid)

	const downID = "cmd-down-reconnect"
	if err := srv.Push(sid, &cloudlinkv1.CloudToEdge{CommandId: downID, SessionId: sid}); err != nil {
		t.Fatalf("Push tras reconexión: se esperaba éxito, got %v", err)
	}
	select {
	case got := <-cliB.Received():
		if got.GetCommandId() != downID {
			t.Errorf("push tras reconexión llegó al stream equivocado: command_id got %q want %q", got.GetCommandId(), downID)
		}
	case <-ctxB.Done():
		t.Fatalf("reconexión: timeout esperando el push en B: %v", ctxB.Err())
	}
}

// TestClientErrPropagation cubre H5/T3: tras cerrarse el stream, el cliente
// expone la causa (no solo "closed"), de modo que el Edge decide su backoff.
func TestClientErrPropagation(t *testing.T) {
	srv, dial := newServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cli := registerSession(t, srv, dial(ctx), ctx, "sess-err")

	// Antes del corte no hay causa: el stream sigue vivo.
	if err := cli.Err(); err != nil {
		t.Fatalf("Err() con stream vivo: se esperaba nil, got %v", err)
	}

	cancel() // corta el stream desde el lado cliente

	select {
	case _, ok := <-cli.Received():
		if ok {
			// Puede llegar un mensaje residual; espera al cierre real.
			<-cli.Received()
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout esperando el cierre del canal Received")
	}

	if err := cli.Err(); err == nil {
		t.Fatal("H5: tras el corte, Err() devolvió nil; el Edge no puede distinguir la causa")
	}
}
