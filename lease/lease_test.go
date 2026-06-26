package lease_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/client"
	"github.com/EduGoGroup/wapp-cloudlink/lease"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fixedNow ancla el reloj para evitar sleeps frágiles: el tiempo se controla con
// expires_unix explícitos (vía ttl + reloj inyectado), no con esperas reales.
var fixedNow = time.Unix(1_700_000_000, 0)

func clockAt(t time.Time) func() time.Time { return func() time.Time { return t } }

// newPair crea Issuer + Validator anclados al mismo reloj y a la misma clave.
func newPair(t *testing.T, now time.Time) (*lease.Issuer, *lease.Validator) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	iss, err := lease.NewIssuer(priv, lease.WithIssuerClock(clockAt(now)))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	val := lease.NewValidator(pub, lease.WithValidatorClock(clockAt(now)))
	return iss, val
}

// TestLeaseVigenteYGate2de2 cubre el estado inicial vigente y el gate 2-de-2.
func TestLeaseVigenteYGate2de2(t *testing.T) {
	iss, val := newPair(t, fixedNow)

	lu, err := iss.Issue("edge-1", "tenant-1", time.Hour, 1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := val.Apply(lu); err != nil {
		t.Fatalf("Apply lease vigente: %v", err)
	}

	if !val.CanOperate(true) {
		t.Error("vigente: CanOperate(hasDEK=true) = false, want true")
	}
	// 2-de-2: sin DEK no opera aunque el lease sea vigente.
	if val.CanOperate(false) {
		t.Error("sin-DEK: CanOperate(hasDEK=false) = true, want false")
	}
}

// TestRevocacionKillSwitch es el núcleo del Criterio 5: la revocación bloquea la
// operación del Edge y es pegajosa.
func TestRevocacionKillSwitch(t *testing.T) {
	iss, val := newPair(t, fixedNow)

	// Arranca vigente.
	luOK, _ := iss.Issue("edge-1", "tenant-1", time.Hour, 1)
	if err := val.Apply(luOK); err != nil {
		t.Fatalf("Apply vigente: %v", err)
	}
	if !val.CanOperate(true) {
		t.Fatal("precondición: debería poder operar antes de revocar")
	}

	// Kill-switch.
	luRev, err := iss.Revoke("edge-1", "tenant-1")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := val.Apply(luRev); err != nil {
		t.Fatalf("Apply revocación: %v", err)
	}
	if val.CanOperate(true) {
		t.Error("revocado: CanOperate(true) = true, want false (operación BLOQUEADA)")
	}

	// Pegajoso: un lease vigente posterior NO des-revoca.
	luNuevo, _ := iss.Issue("edge-1", "tenant-1", time.Hour, 99)
	if err := val.Apply(luNuevo); err != nil {
		t.Fatalf("Apply post-revocación: %v", err)
	}
	if val.CanOperate(true) {
		t.Error("post-revocación: un lease vigente NO debe des-revocar (sticky)")
	}
}

// TestLeaseExpirado: lease con expires_unix en el pasado no opera.
func TestLeaseExpirado(t *testing.T) {
	iss, val := newPair(t, fixedNow)

	// ttl negativo => expires en el pasado relativo al reloj fijo.
	lu, err := iss.Issue("edge-1", "tenant-1", -time.Minute, 1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := val.Apply(lu); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if val.CanOperate(true) {
		t.Error("expirado: CanOperate(true) = true, want false")
	}
}

// TestFirmaInvalida: lease firmado por otra clave o manipulado => Apply error y
// el estado NO pasa a vigente.
func TestFirmaInvalida(t *testing.T) {
	// Emisor con clave A; validador con clave B (distinta).
	issA, _ := newPair(t, fixedNow)
	_, valB := newPair(t, fixedNow)

	luOtraClave, _ := issA.Issue("edge-1", "tenant-1", time.Hour, 1)
	if err := valB.Apply(luOtraClave); err == nil {
		t.Error("firma de otra clave: Apply = nil, want error")
	}
	if valB.CanOperate(true) {
		t.Error("firma de otra clave: el estado pasó a vigente; debe seguir bloqueado")
	}

	// Manipulación del blob: corromper un byte del payload firmado.
	issC, valC := newPair(t, fixedNow)
	lu, _ := issC.Issue("edge-1", "tenant-1", time.Hour, 1)
	tampered := append([]byte(nil), lu.GetLease()...)
	tampered[len(tampered)/2] ^= 0xFF
	luTampered := &cloudlinkv1.LeaseUpdate{Lease: tampered, ExpiresUnix: lu.GetExpiresUnix()}
	if err := valC.Apply(luTampered); err == nil {
		t.Error("blob manipulado: Apply = nil, want error")
	}
	if valC.CanOperate(true) {
		t.Error("blob manipulado: el estado pasó a vigente; debe seguir bloqueado")
	}
}

// TestAntiReplayCounter: tras un counter nuevo, uno más viejo se rechaza y no
// altera el estado vigente.
func TestAntiReplayCounter(t *testing.T) {
	iss, val := newPair(t, fixedNow)

	luNuevo, _ := iss.Issue("edge-1", "tenant-1", time.Hour, 5)
	if err := val.Apply(luNuevo); err != nil {
		t.Fatalf("Apply counter=5: %v", err)
	}

	luViejo, _ := iss.Issue("edge-1", "tenant-1", time.Hour, 3)
	if err := val.Apply(luViejo); err == nil {
		t.Error("replay counter=3 tras 5: Apply = nil, want ErrStaleCounter")
	}
	// El lease vigente (counter=5) sigue válido pese al intento de replay.
	if !val.CanOperate(true) {
		t.Error("tras replay rechazado: CanOperate(true) = false, want true (estado intacto)")
	}
}

// TestE2ERevocacionPorStream: end-to-end sobre bufconn. El servidor empuja un
// LeaseUpdate(revoked) por el stream Connect; el lado cliente lo entrega al
// Validator, que bloquea la operación. Cierra el lazo Issuer -> stream -> Validator.
func TestE2ERevocacionPorStream(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	iss, err := lease.NewIssuer(priv, lease.WithIssuerClock(clockAt(fixedNow)))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	val := lease.NewValidator(pub, lease.WithValidatorClock(clockAt(fixedNow)))

	// Transporte bufconn (in-memory, sin TLS).
	lis := bufconn.Listen(1024 * 1024)
	srv := server.New()
	gs := grpc.NewServer()
	cloudlinkv1.RegisterCloudLinkServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }
	cc, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	const sessionID = "sess-kill"

	// El Edge emite algo para registrar la sesión (destino del push).
	if err := cli.Send(&cloudlinkv1.EdgeToCloud{
		SessionId: sessionID,
		Payload:   &cloudlinkv1.EdgeToCloud_Heartbeat{Heartbeat: &cloudlinkv1.Heartbeat{LeaseCounter: 1}},
	}); err != nil {
		t.Fatalf("cli.Send: %v", err)
	}
	// Espera a que el servidor observe la sesión (drena el canal de recepción).
	select {
	case <-srv.Received():
	case <-ctx.Done():
		t.Fatalf("timeout registrando sesión: %v", ctx.Err())
	}

	// El servidor dispara el kill-switch por el stream.
	luRev, err := iss.Revoke("edge-1", "tenant-1")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// La sesión ya está registrada: register() ocurre ANTES del envío al canal
	// Received, así que tras drenarlo el destino del push existe (sin carrera).
	if err := srv.PushLease(sessionID, luRev); err != nil {
		t.Fatalf("PushLease: %v", err)
	}

	// El Edge recibe el LeaseUpdate y lo aplica al Validator.
	select {
	case cmd := <-cli.Received():
		lu := cmd.GetLeaseUpdate()
		if lu == nil {
			t.Fatalf("cloud->edge: se esperaba LeaseUpdate, got %T", cmd.GetPayload())
		}
		if err := val.Apply(lu); err != nil {
			t.Fatalf("Apply revocación recibida: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout esperando LeaseUpdate en el cliente: %v", ctx.Err())
	}

	if val.CanOperate(true) {
		t.Error("e2e: tras revocación por stream, CanOperate(true) = true, want false (BLOQUEADO)")
	}
}
