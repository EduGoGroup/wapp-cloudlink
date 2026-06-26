// Package mtls construye las transport-credentials de gRPC para el canal
// CloudLink.Connect: mTLS con cert por Edge firmado por la CA de la
// plataforma/tenant. Es un concern de transporte a nivel de
// grpc.NewServer/grpc.NewClient, NO de la lógica de server.New(); así el
// transporte insecure de pruebas sigue funcionando sin cambios.
//
// El material privado (claves, certs de dev) vive fuera de git (ver
// scripts/gen-dev-certs.sh y .gitignore). Los tests generan certs efímeros en
// memoria, no dependen de archivos.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// ServerCreds construye las credentials del lado cloud para CloudLink.Connect.
// Exige y verifica el cert de cliente (Edge) contra clientCAs: solo Edges con
// cert firmado por la CA aceptada completan el handshake.
func ServerCreds(serverCert tls.Certificate, clientCAs *x509.CertPool) credentials.TransportCredentials {
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	return credentials.NewTLS(cfg)
}

// ClientCreds construye las credentials del lado Edge: presenta su cert y valida
// el cert del servidor contra rootCAs. serverName debe coincidir con un SAN del
// cert de servidor (p.ej. "localhost").
func ClientCreds(clientCert tls.Certificate, rootCAs *x509.CertPool, serverName string) credentials.TransportCredentials {
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      rootCAs,
		ServerName:   serverName,
	}
	return credentials.NewTLS(cfg)
}

// LoadServerCredsFromFiles es el cableado de producción/dev a partir de archivos
// (los que genera scripts/gen-dev-certs.sh). caFile es la CA que firma los certs
// de Edge.
func LoadServerCredsFromFiles(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: cargar cert de servidor: %w", err)
	}
	pool, err := loadCertPool(caFile)
	if err != nil {
		return nil, err
	}
	return ServerCreds(cert, pool), nil
}

// LoadClientCredsFromFiles es el cableado del lado Edge a partir de archivos.
func LoadClientCredsFromFiles(certFile, keyFile, caFile, serverName string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: cargar cert de cliente: %w", err)
	}
	pool, err := loadCertPool(caFile)
	if err != nil {
		return nil, err
	}
	return ClientCreds(cert, pool, serverName), nil
}

func loadCertPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: leer CA %q: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("mtls: CA %q no contiene certificados PEM válidos", caFile)
	}
	return pool, nil
}
