package server_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/client"
	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"github.com/EduGoGroup/wapp-cloudlink/lease"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// startCloudLink monta un servidor CloudLink con las opciones dadas sobre bufconn
// y devuelve el server y un ClientConn conectado.
func startCloudLink(t *testing.T, opts ...server.Option) (*server.Server, grpc.ClientConnInterface) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := server.New(opts...)
	gs := grpc.NewServer()
	cloudlinkv1.RegisterCloudLinkServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

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
	return srv, cc
}

// TestHeartbeatRenewsLease cubre T8/H10: con WithLeaseRenewal, un Heartbeat con
// lease_counter=N hace que el servidor emita y empuje un lease renovado
// (counter=N+1) por el mismo stream, que el Validator del Edge acepta.
func TestHeartbeatRenewsLease(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	iss, err := lease.NewIssuer(priv)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	val := lease.NewValidator(pub)

	srv, cc := startCloudLink(t, server.WithLeaseRenewal(iss, time.Hour))
	_ = srv

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Heartbeat con counter=7 => el servidor renueva a counter=8 y lo empuja.
	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		SessionId: "sess-hb",
		Payload:   &cloudlinkv1.EdgeToCloud_Heartbeat{Heartbeat: &cloudlinkv1.Heartbeat{LeaseCounter: 7}},
	}); err != nil {
		t.Fatalf("cli.Send(heartbeat): %v", err)
	}

	select {
	case cmd := <-cli.Received():
		lu := cmd.GetLeaseUpdate()
		if lu == nil {
			t.Fatalf("se esperaba un LeaseUpdate renovado, got %T", cmd.GetPayload())
		}
		if err := val.Apply(lu); err != nil {
			t.Fatalf("Apply lease renovado: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout esperando el lease renovado: %v", ctx.Err())
	}

	if !val.CanOperate(true) {
		t.Error("tras la renovación por heartbeat, el Edge debería poder operar")
	}
	// El counter renovado fue 8: un lease posterior con counter=8 es replay y se
	// rechaza, lo que confirma que se aplicó exactamente 7+1.
	luStale, err := iss.Issue("sess-hb", "", time.Hour, 8)
	if err != nil {
		t.Fatalf("Issue(stale): %v", err)
	}
	if err := val.Apply(luStale); err == nil {
		t.Error("counter=8 tras renovación a 8: se esperaba ErrStaleCounter, got nil")
	}
}

// TestHeartbeatNoRenewalWithoutIssuer cubre el camino sin LeaseIssuer: un
// Heartbeat NO dispara ningún push (el servidor solo lo drena a Received()).
func TestHeartbeatNoRenewalWithoutIssuer(t *testing.T) {
	srv, cc := startCloudLink(t) // sin WithLeaseRenewal

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		SessionId: "sess-noiss",
		Payload:   &cloudlinkv1.EdgeToCloud_Heartbeat{Heartbeat: &cloudlinkv1.Heartbeat{LeaseCounter: 1}},
	}); err != nil {
		t.Fatalf("cli.Send(heartbeat): %v", err)
	}

	// El servidor procesa el heartbeat (drena Received): garantiza que si fuese a
	// renovar, ya lo habría hecho antes de que comprobemos la ausencia de push.
	select {
	case <-srv.Received():
	case <-ctx.Done():
		t.Fatalf("timeout drenando el heartbeat: %v", ctx.Err())
	}

	select {
	case cmd := <-cli.Received():
		t.Fatalf("sin LeaseIssuer no debía haber push, got %T", cmd.GetPayload())
	case <-time.After(200 * time.Millisecond):
		// OK: no hubo renovación.
	}
}
