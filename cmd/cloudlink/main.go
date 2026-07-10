package main

import (
	"log"
	"net"
	"os"
	"path/filepath"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"github.com/EduGoGroup/wapp-cloudlink/mtls"
	"github.com/EduGoGroup/wapp-cloudlink/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"time"
)

// keepaliveOpts refleja el keepalive de transporte de la Plataforma Cloud (Plan
// 026 · T3): el stream Connect es bidi long-lived 24/7 tras NAT/firewalls
// domésticos; sin keepalive un corte silencioso de NAT deja el stream
// medio-abierto (agrava la fuga de sesiones H1). El servidor hace PING cada
// Time=30s (Timeout=10s para declarar muerto el transporte) y la
// EnforcementPolicy admite los PING del cliente Edge con MinTime=15s +
// PermitWithoutStream. Parámetros IDÉNTICOS a wapp-cloud-platform para que el
// cliente Edge no reciba GOAWAY too_many_pings de un extremo y no del otro.
func keepaliveOpts() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             15 * time.Second,
			PermitWithoutStream: true,
		}),
	}
}

// main ensambla un servidor CloudLink de demostración. Si existen los certs de
// dev (ver scripts/gen-dev-certs.sh) arranca con mTLS; si no, arranca inseguro
// para no bloquear el dev sin PKI. El flujo de enrolamiento (T4) y el lease (T5)
// son posteriores.
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	addr := envOr("CLOUDLINK_ADDR", ":8101")
	certDir := envOr("CLOUDLINK_CERT_DIR", "certs")

	opts := append(keepaliveOpts(), transport.ServerOptions()...)
	caFile := filepath.Join(certDir, "ca.crt")
	certFile := filepath.Join(certDir, "server.crt")
	keyFile := filepath.Join(certDir, "server.key")
	if fileExists(caFile) && fileExists(certFile) && fileExists(keyFile) {
		creds, err := mtls.LoadServerCredsFromFiles(certFile, keyFile, caFile)
		if err != nil {
			log.Fatalf("cloudlink: certs presentes pero inválidos: %v", err)
		}
		opts = append(opts, grpc.Creds(creds))
		log.Printf("cloudlink: mTLS activo (certs en %q)", certDir)
	} else {
		log.Printf("cloudlink: SIN mTLS (no se hallaron certs en %q); ejecuta scripts/gen-dev-certs.sh", certDir)
	}

	gs := grpc.NewServer(opts...)
	srv := server.New(server.WithSaturationHook(func(sessionID string, dropped uint64) {
		log.Printf("cloudlink: SATURACIÓN sesión=%q descartados=%d (consumidor lento)", sessionID, dropped)
	}))
	cloudlinkv1.RegisterCloudLinkServer(gs, srv)
	cloudlinkv1.RegisterEnrollmentServer(gs, srv)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("cloudlink: escuchar en %s: %v", addr, err)
	}
	log.Printf("cloudlink: escuchando en %s", addr)
	if err := gs.Serve(lis); err != nil {
		log.Fatalf("cloudlink: serve: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
