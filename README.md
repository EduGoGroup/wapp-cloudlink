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

## Estado

**Greenfield.** Solo scaffold inicial. Ver `CLAUDE.md` para contexto
arquitectónico y `../../docs/piezas/02-cloudlink.md` para la especificación.

> El module path `github.com/wApp/wapp-cloudlink` es un placeholder ajustable
> al repositorio Git real cuando se publique.
