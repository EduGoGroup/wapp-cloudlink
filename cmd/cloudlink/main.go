package main

import (
	"log"
	"net"
	"os"
	"path/filepath"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/mtls"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"google.golang.org/grpc"
)

// main ensambla un servidor CloudLink de demostración. Si existen los certs de
// dev (ver scripts/gen-dev-certs.sh) arranca con mTLS; si no, arranca inseguro
// para no bloquear el dev sin PKI. El flujo de enrolamiento (T4) y el lease (T5)
// son posteriores.
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	addr := envOr("CLOUDLINK_ADDR", ":8443")
	certDir := envOr("CLOUDLINK_CERT_DIR", "certs")

	var opts []grpc.ServerOption
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
	srv := server.New()
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
