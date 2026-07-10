// Package transport centraliza los límites y opciones de transporte gRPC del
// canal CloudLink para que el servidor (nube) y el cliente (Edge) compartan UNA
// única fuente de verdad (T7/H4). No es internal a propósito: el Edge (repo
// aparte) importa DialOptions para dialar con los mismos límites que impone el
// servidor.
package transport

import "google.golang.org/grpc"

// MaxMessageBytes es el tamaño máximo de un mensaje del stream Connect y de
// EnrollEdge, en AMBOS sentidos y en AMBOS extremos. Acota la memoria por mensaje
// y evita que un frame gigante monopolice el transporte.
//
// Relación con la media (SendMedia, ADR-0005): SendMedia lleva la carga como
// `inline` (bytes) o como `presigned_url`. La media que supere este límite DEBE
// viajar como presigned_url (patrón R2 del Plan 017), no inline. Además de superar
// el límite, un `inline` grande bloquearía bajo el sendMu de la sesión a los Pings
// y al LeaseUpdate (kill-switch), otra razón para preferir la URL prefirmada. El
// presigned NO se implementa aquí (ya existe en el contrato y en la Plataforma);
// este límite solo lo hace exigible.
const MaxMessageBytes = 4 << 20 // 4 MiB

// ServerOptions son las opciones de límite para grpc.NewServer (lado nube).
// Combínalas con las de creds/keepalive del cmd correspondiente.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(MaxMessageBytes),
		grpc.MaxSendMsgSize(MaxMessageBytes),
	}
}

// DialOptions son las opciones de límite para grpc.NewClient (lado Edge), espejo
// de ServerOptions para que ambos extremos coincidan.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxMessageBytes),
			grpc.MaxCallSendMsgSize(MaxMessageBytes),
		),
	}
}
