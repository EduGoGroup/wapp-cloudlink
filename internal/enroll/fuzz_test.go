package enroll_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"

	"github.com/EduGoGroup/wapp-cloudlink/internal/enroll"
)

// FuzzParseAndVerifyCSR ejercita el parseo/verificación del CSR con entradas
// arbitrarias (T8/H10): ningún PEM/DER malformado debe provocar panic;
// ParseAndVerifyCSR debe devolver siempre (*CertificateRequest, error) de forma
// controlada. El corpus semilla incluye un CSR válido y varios degenerados.
//
// Los casos F* corren como unit con el corpus en CI; el fuzzing largo
// (go test -fuzz=FuzzParseAndVerifyCSR) es manual.
func FuzzParseAndVerifyCSR(f *testing.F) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "edge-seed"},
	}, key)
	if err != nil {
		f.Fatalf("CreateCertificateRequest: %v", err)
	}
	validPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})

	f.Add(validPEM)                                                                   // CSR válido
	f.Add([]byte(""))                                                                 // vacío
	f.Add([]byte("garbage not pem"))                                                  // no-PEM
	f.Add([]byte("-----BEGIN CERTIFICATE REQUEST-----\nYWJj\n-----END CERTIFICATE REQUEST-----")) // PEM con DER basura
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nYWJj\n-----END PRIVATE KEY-----"))     // tipo de bloque equivocado
	f.Add(validPEM[:len(validPEM)/2])                                                 // truncado

	f.Fuzz(func(t *testing.T, csrPEM []byte) {
		// Contrato: no panic ante cualquier entrada.
		_, _ = enroll.ParseAndVerifyCSR(csrPEM)
	})
}
