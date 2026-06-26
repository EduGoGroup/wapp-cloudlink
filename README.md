# wapp-cloudlink (Pieza 02)

Contrato y conducto entre el Edge Agent y la Plataforma Cloud. Define el
esquema protobuf y contiene el servidor gRPC del lado cloud (puede vivir como
módulo dentro de `wapp-cloud-platform` o extraerse a servicio propio).

## Rol en wApp

Es el **único canal** entre el Edge (cliente) y la nube (nosotros). Conexión
**saliente iniciada por el Edge** (cero config de NAT/firewall para el usuario)
sobre gRPC bidi-stream con mTLS. Por aquí viajan órdenes de despacho
(cloud→edge), eventos entrantes y estados (edge→cloud), y el lease operativo.
**Nunca** viajan la DEK, el store cifrado ni las llaves Signal.

## Tecnología

| Decisión | Detalle |
|---|---|
| Lenguaje | Go 1.23 |
| Transporte | gRPC bidi-stream sobre HTTP/2 |
| Seguridad | mTLS (cert por Edge/tenant) + token de plataforma |
| Contrato | Protobuf (`.proto` en `proto/`) |
| Enrolamiento | `Enrollment.EnrollEdge` (unario, TLS de servidor + código de un solo uso) |
| Stream principal | `CloudLink.Connect` (bidi-stream persistente, mTLS) |
| Multiplexado | Por `session_id` (un Edge gestiona N teléfonos) |
| Resiliencia | Backoff exponencial en el Edge + `outbox` SQLite |

## Código generado

El código protobuf/gRPC generado **se commitea** y vive en
`gen/wapp/cloudlink/v1` (paquete `cloudlinkv1`) — **nunca** bajo `internal/`.
Motivo: el Edge debe importar el cliente generado **cross-repo**, y los paquetes
`internal/` son inimportables fuera del módulo. La generación es reproducible
con `buf generate` (config en `buf.gen.yaml`, sin managed mode: el `go_package`
se declara explícito en el `.proto`).

## Cómo correrá (placeholder)

```bash
# Compilar (placeholder — puede ser un binario standalone o importarse como módulo)
go build -o bin/cloudlink ./cmd/cloudlink

# Generar código protobuf (placeholder)
# protoc --go_out=. --go-grpc_out=. proto/cloudlink.proto
```

## PKI de desarrollo / mTLS

El canal `CloudLink.Connect` usa **mTLS**: cert por Edge firmado por la CA de la
plataforma/tenant (en dev, una CA local). Las transport-credentials viven en
`internal/mtls` (`ServerCreds` / `ClientCreds`); es un concern de transporte a
nivel de `grpc.NewServer`/`grpc.NewClient`, fuera de la lógica de `server.New()`.

Para generar la PKI local (CA + cert de servidor + cert de Edge):

```bash
./scripts/gen-dev-certs.sh                  # CN de Edge: edge-dev-001
EDGE_CN=edge-acme-7 ./scripts/gen-dev-certs.sh
```

Genera en `certs/`: `ca.crt`/`ca.key`, `server.crt`/`server.key`
(SAN `localhost`/`127.0.0.1`) y `edge.crt`/`edge.key`. El script es idempotente.

**`certs/` y todo material privado (`*.key`, `*.pem`) están fuera de git** y
**nunca** se committean (ver `.gitignore`). En producción los certs de Edge los
emite la CA del tenant vía el flujo de enrolamiento. El binario `cmd/cloudlink`
arranca con mTLS si halla los certs en `certs/`, y sin TLS si no (solo dev).

## Estado

**Greenfield.** Solo scaffold inicial. Ver `CLAUDE.md` para contexto
arquitectónico y `../../docs/piezas/02-cloudlink.md` para la especificación.

> El module path `github.com/wApp/wapp-cloudlink` es un placeholder ajustable
> al repositorio Git real cuando se publique.
