package enroll_test

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/client"
	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/enroll"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"github.com/EduGoGroup/wapp-cloudlink/mtls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// startEnrollServer levanta un endpoint Enrollment SIN mTLS (insecure sobre
// bufconn): el Edge aún no tiene cert. Devuelve un ClientConn ya conectado.
func startEnrollServer(t *testing.T, svc *enroll.Service) grpc.ClientConnInterface {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	cloudlinkv1.RegisterEnrollmentServer(gs, server.New(server.WithEnroller(svc)))
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
		t.Fatalf("grpc.NewClient (enroll): %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

// startMTLSServer levanta un endpoint CloudLink con mTLS y devuelve un dialer.
func startMTLSServer(t *testing.T, creds credentials.TransportCredentials) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer(grpc.Creds(creds))
	cloudlinkv1.RegisterCloudLinkServer(gs, server.New())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestEnrollThenMTLS es el HITO (Criterio de éxito 4): un Edge sin cert se enrola
// con un código válido y luego conecta por mTLS usando el cert recibido,
// validado contra la MISMA CA. Liga T3 (mTLS) + T4 (enrolamiento).
func TestEnrollThenMTLS(t *testing.T) {
	ca, err := enroll.NewDevCA("wapp-dev-ca", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("NewDevCA: %v", err)
	}
	store := enroll.NewMemoryStore()
	store.Add("CODE-OK", "tenant-42", time.Now().Add(time.Hour))
	svc := enroll.NewService(store, ca)

	// --- Enrolamiento (sin mTLS) ---
	enrollCC := startEnrollServer(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	enrolled, err := enroll.EnrollClient(ctx, enrollCC, "CODE-OK", enroll.EdgeIdentity{CommonName: "edge-001"})
	if err != nil {
		t.Fatalf("EnrollClient (OK): %v", err)
	}
	if enrolled.TenantID != "tenant-42" {
		t.Errorf("tenant_id: got %q, want %q", enrolled.TenantID, "tenant-42")
	}
	if enrolled.Certificate.Leaf == nil || enrolled.Certificate.Leaf.Subject.CommonName != "edge-001" {
		t.Fatalf("cert de Edge inesperado: %+v", enrolled.Certificate.Leaf)
	}

	// --- Conexión mTLS con el cert enrolado, contra la MISMA CA ---
	srvCertPEM, srvKeyPEM, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	serverCert, err := tls.X509KeyPair(srvCertPEM, srvKeyPEM)
	if err != nil {
		t.Fatalf("cargar cert de servidor: %v", err)
	}

	// El servidor exige cert de cliente firmado por la CA del enrolamiento.
	dialer := startMTLSServer(t, mtls.ServerCreds(serverCert, ca.Pool()))
	// El cliente presenta el cert enrolado y confía en la CA devuelta.
	clientCreds := mtls.ClientCreds(enrolled.Certificate, enrolled.CAPool, "localhost")

	cc, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(clientCreds),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient (mtls): %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	// Forzar el handshake mTLS con un RPC real: si el cert enrolado no fuese
	// aceptable, este Send fallaría.
	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		CommandId: "cmd-1",
		SessionId: "sess-1",
		Payload:   &cloudlinkv1.EdgeToCloud_Heartbeat{Heartbeat: &cloudlinkv1.Heartbeat{}},
	}); err != nil {
		t.Fatalf("mTLS con cert enrolado: Send falló: %v", err)
	}
}

// TestEnrollInvalidCode: código desconocido -> PermissionDenied.
func TestEnrollInvalidCode(t *testing.T) {
	ca, err := enroll.NewDevCA("wapp-dev-ca", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("NewDevCA: %v", err)
	}
	svc := enroll.NewService(enroll.NewMemoryStore(), ca) // store vacío
	cc := startEnrollServer(t, svc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = enroll.EnrollClient(ctx, cc, "DESCONOCIDO", enroll.EdgeIdentity{CommonName: "edge-x"})
	assertCode(t, err, codes.PermissionDenied, "código desconocido")
}

// TestEnrollExpiredCode: código con TTL pasado -> PermissionDenied.
func TestEnrollExpiredCode(t *testing.T) {
	ca, err := enroll.NewDevCA("wapp-dev-ca", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("NewDevCA: %v", err)
	}
	store := enroll.NewMemoryStore()
	store.Add("CODE-EXP", "tenant-42", time.Now().Add(-time.Minute)) // ya expirado
	cc := startEnrollServer(t, enroll.NewService(store, ca))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = enroll.EnrollClient(ctx, cc, "CODE-EXP", enroll.EdgeIdentity{CommonName: "edge-x"})
	assertCode(t, err, codes.PermissionDenied, "código expirado")
}

// TestEnrollUsedCode: segundo uso del mismo código -> PermissionDenied.
func TestEnrollUsedCode(t *testing.T) {
	ca, err := enroll.NewDevCA("wapp-dev-ca", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("NewDevCA: %v", err)
	}
	store := enroll.NewMemoryStore()
	store.Add("CODE-1USE", "tenant-42", time.Now().Add(time.Hour))
	cc := startEnrollServer(t, enroll.NewService(store, ca))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err = enroll.EnrollClient(ctx, cc, "CODE-1USE", enroll.EdgeIdentity{CommonName: "edge-a"}); err != nil {
		t.Fatalf("primer uso debería funcionar: %v", err)
	}
	_, err = enroll.EnrollClient(ctx, cc, "CODE-1USE", enroll.EdgeIdentity{CommonName: "edge-b"})
	assertCode(t, err, codes.PermissionDenied, "código ya usado")
}

func assertCode(t *testing.T, err error, want codes.Code, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: se esperaba error %v, got nil", what, want)
	}
	if got := status.Code(err); got != want {
		t.Fatalf("%s: código gRPC got %v, want %v (err=%v)", what, got, want, err)
	}
}
