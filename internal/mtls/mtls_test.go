package mtls_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/internal/client"
	"github.com/EduGoGroup/wapp-cloudlink/internal/mtls"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"
)

// --- PKI efímera en memoria (no toca disco ni claves committeadas) ---

type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

// newCA crea una CA autofirmada efímera.
func newCA(t *testing.T, cn string) *ca {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("newCA: generar clave: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("newCA: crear cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("newCA: parsear cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &ca{cert: cert, key: key, pool: pool}
}

// issue firma un cert hoja (servidor o Edge) con esta CA.
func (c *ca) issue(t *testing.T, cn string, dnsNames []string, ips []net.IP, serverAuth bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("issue: generar clave: %v", err)
	}
	eku := x509.ExtKeyUsageClientAuth
	if serverAuth {
		eku = x509.ExtKeyUsageServerAuth
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{eku},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatalf("issue: crear cert: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        mustParse(t, der),
	}
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsear hoja: %v", err)
	}
	return cert
}

// startServer arranca un servidor CloudLink con mTLS sobre bufconn y devuelve un
// dialer para construir clientes contra él.
func startServer(t *testing.T, serverCreds credentials.TransportCredentials) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer(grpc.Creds(serverCreds))
	cloudlinkv1.RegisterCloudLinkServer(gs, server.New())
	cloudlinkv1.RegisterEnrollmentServer(gs, server.New())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// dial construye un gRPC ClientConn sobre el dialer bufconn con las credentials
// dadas. NewClient es lazy: el handshake real se fuerza con el primer RPC.
func dial(t *testing.T, dialer func(context.Context, string) (net.Conn, error), creds credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	cc, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

// TestMTLSConnect cubre el Criterio de éxito 3: handshake mTLS sobre bufconn.
//   - OK: Edge con cert válido firmado por la CA -> stream Connect funciona.
//   - sin-cert: cliente sin cert de cliente -> RECHAZADO.
//   - CA-rival: cliente con cert firmado por otra CA -> RECHAZADO.
func TestMTLSConnect(t *testing.T) {
	caDev := newCA(t, "wapp-dev-ca")
	caRival := newCA(t, "rival-ca")

	serverCert := caDev.issue(t, "localhost",
		[]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, true)
	edgeCert := caDev.issue(t, "edge-001", nil, nil, false)
	rivalCert := caRival.issue(t, "edge-rogue", nil, nil, false)

	// El servidor exige cert de cliente firmado por la CA de dev.
	serverCreds := mtls.ServerCreds(serverCert, caDev.pool)

	t.Run("OK", func(t *testing.T) {
		dialer := startServer(t, serverCreds)
		creds := mtls.ClientCreds(edgeCert, caDev.pool, "localhost")
		cc := dial(t, dialer, creds)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cli, err := client.New(ctx, cc)
		if err != nil {
			t.Fatalf("client.New: %v", err)
		}
		// Forzar el handshake con un RPC real edge->cloud.
		if err := cli.Send(&cloudlinkv1.EdgeToCloud{
			CommandId: "cmd-1",
			SessionId: "sess-1",
			Payload: &cloudlinkv1.EdgeToCloud_Heartbeat{
				Heartbeat: &cloudlinkv1.Heartbeat{},
			},
		}); err != nil {
			t.Fatalf("mTLS válido: Send falló inesperadamente: %v", err)
		}
	})

	t.Run("sin-cert", func(t *testing.T) {
		dialer := startServer(t, serverCreds)
		// Cliente que confía en la CA del servidor pero NO presenta cert propio.
		creds := credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    caDev.pool,
			ServerName: "localhost",
		})
		cc := dial(t, dialer, creds)
		assertRejected(t, cc, "cliente sin cert")
	})

	t.Run("CA-rival", func(t *testing.T) {
		dialer := startServer(t, serverCreds)
		// El cliente confía en la CA del servidor pero presenta cert de otra CA.
		creds := mtls.ClientCreds(rivalCert, caDev.pool, "localhost")
		cc := dial(t, dialer, creds)
		assertRejected(t, cc, "cliente con cert de CA rival")
	})
}

// assertRejected fuerza el handshake con el primer RPC y exige que falle. gRPC
// es lazy: el rechazo del handshake mTLS aflora en el primer Send/Recv del
// stream, no en NewClient. La señal fiable de rechazo es el cierre del canal
// Received() (el recvLoop observa el stream roto y lo cierra). Usamos un
// contexto con timeout para no colgar y que el caso sea determinista.
func assertRejected(t *testing.T, cc *grpc.ClientConn, what string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := client.New(ctx, cc)
	if err != nil {
		// Rechazo aflorado ya al abrir el stream: válido.
		return
	}
	// Si el envío detecta el stream roto, ya es rechazo.
	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		CommandId: "cmd-x",
		SessionId: "sess-x",
		Payload: &cloudlinkv1.EdgeToCloud_Heartbeat{
			Heartbeat: &cloudlinkv1.Heartbeat{},
		},
	}); err != nil {
		return
	}
	// Si no, el recvLoop cerrará Received() al fallar el handshake.
	select {
	case _, ok := <-cli.Received():
		if !ok {
			return // canal cerrado -> stream caído -> handshake rechazado
		}
		t.Fatalf("%s: se recibió un mensaje inesperado; se esperaba rechazo mTLS", what)
	case <-ctx.Done():
		t.Fatalf("%s: timeout sin rechazo claro del handshake mTLS: %v", what, ctx.Err())
	}
}
