package main

import (
	"fmt"
	"log"
)

func main() {
	// TODO: Inicializar el servidor CloudLink (punto de terminación gRPC)
	// - Cargar certificados TLS del servidor + CA para validar certs de Edges (mTLS)
	// - Registrar servicios gRPC: Enrollment.EnrollEdge (unario) + CloudLink.Connect (bidi-stream)
	// - Arrancar servidor gRPC con mTLS
	// - Bloquear en loop de señales del SO (SIGTERM/SIGINT → graceful shutdown)
	//
	// Nota: este binario puede ejecutarse embebido dentro de wapp-cloud-platform
	// (módulo Gateway) o como proceso separado si se extrae más adelante (ADR-0010).

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	fmt.Println("wapp-cloudlink: placeholder — sin lógica aún")
	log.Println("TODO: implementar servidor gRPC CloudLink (bidi-stream + mTLS)")
}
