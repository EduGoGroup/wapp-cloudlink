package enroll

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"google.golang.org/grpc"
)

// EdgeIdentity describe la identidad que el Edge solicita en su CSR.
type EdgeIdentity struct {
	// CommonName: identidad del Edge (p.ej. su edge_id). Obligatorio.
	CommonName string
	// Organization/OrganizationalUnit: metadatos opcionales del CSR.
	Organization       []string
	OrganizationalUnit []string
}

// Enrolled es el resultado del enrolamiento del lado cliente: el cert listo para
// mTLS (clave privada local + cert recibido) y el pool de la CA para validar al
// servidor.
type Enrolled struct {
	// Certificate combina la clave privada generada localmente y el cert hoja
	// firmado por la CA. Listo para mtls.ClientCreds.
	Certificate tls.Certificate
	// CAPool contiene la cadena de la CA devuelta por el servidor (RootCAs).
	CAPool *x509.CertPool
	// TenantID asociado al Edge enrolado.
	TenantID string
	// CloudEncPubkey es la pública X25519 (32B) de cifrado de la nube: el Edge la
	// usa para sellar los campos sensibles (SealFor -> enc_payload). Puede ir
	// vacía si el servidor no la configuró (T6/H8).
	CloudEncPubkey []byte
}

// EnrollClient modela lo que hará el Edge en T6: genera un par ECDSA P-256 (la
// clave privada NUNCA sale del proceso), construye un CSR con la identidad dada,
// llama a Enrollment.EnrollEdge con el código de activación y devuelve la
// tls.Certificate lista para conectar por mTLS más el pool de la CA.
//
// Vive en cloudlink (no en el Edge): el cableado real en el Edge es T6.
func EnrollClient(ctx context.Context, cc grpc.ClientConnInterface, activationCode string, id EdgeIdentity) (*Enrolled, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("enroll: generar clave del Edge: %w", err)
	}
	csrPEM, err := buildCSR(key, id)
	if err != nil {
		return nil, err
	}

	resp, err := cloudlinkv1.NewEnrollmentClient(cc).EnrollEdge(ctx, &cloudlinkv1.EnrollEdgeRequest{
		ActivationCode: activationCode,
		CsrPem:         csrPEM,
	})
	if err != nil {
		return nil, err
	}

	leafDER, leaf, err := decodeLeaf(resp.GetEdgeCertPem())
	if err != nil {
		return nil, err
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(resp.GetCaChainPem()) {
		return nil, errors.New("enroll: ca_chain_pem no contiene certificados PEM válidos")
	}

	return &Enrolled{
		Certificate: tls.Certificate{
			Certificate: [][]byte{leafDER},
			PrivateKey:  key,
			Leaf:        leaf,
		},
		CAPool:         caPool,
		TenantID:       resp.GetTenantId(),
		CloudEncPubkey: resp.GetCloudEncPubkey(),
	}, nil
}

func buildCSR(key *ecdsa.PrivateKey, id EdgeIdentity) ([]byte, error) {
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:         id.CommonName,
			Organization:       id.Organization,
			OrganizationalUnit: id.OrganizationalUnit,
		},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("enroll: crear CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCSR, Bytes: der}), nil
}

// decodeLeaf toma el primer bloque CERTIFICATE del PEM recibido como cert hoja.
func decodeLeaf(certPEM []byte) ([]byte, *x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != pemTypeCert {
		return nil, nil, errors.New("enroll: edge_cert_pem no es un CERTIFICATE válido")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("enroll: parsear cert de Edge: %w", err)
	}
	return block.Bytes, leaf, nil
}
