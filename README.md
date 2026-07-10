# wapp-cloudlink (Pieza 02)

Contrato y conducto entre el Edge Agent y la Plataforma Cloud. Define el
esquema protobuf (fuente de verdad del contrato) y el cliente Go del lado Edge,
más una **implementación de referencia** del servidor gRPC del lado cloud.

## Frontera servidor: referencia vs. producción (Plan 027 · Ola 0 · T4)

`internal/server` es una **implementación de referencia / demo**, no el servidor
de producción. El servidor CloudLink real que terminan los Edges vive en la
**Plataforma Cloud** (`wapp-cloud-platform`, paquete `internal/gateway/grpc`):
ahí están el mTLS estricto, el fleet, la persistencia de leases y los dos
listeners. La Plataforma **no importa** este paquete —tiene su propia
implementación— y por eso `internal/server` vive bajo `internal/` a propósito:
es inimportable cross-repo. Su razón de ser es (a) validar el contrato proto
extremo a extremo, (b) alimentar los arneses e2e de esta pieza
(`cmd/cloudlink`, `cmd/democloud`) y (c) servir de referencia legible del ciclo
de vida del stream. Lo que este repo **exporta** para consumo externo es el
contrato (`gen/`), el cliente (`client/`) y el lease (`internal/lease` modela
ambos lados). Decisión: **no** se extrae el servidor a un paquete compartido.

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

## Lease operativo / kill-switch (ADR-0007)

El **lease** autoriza al Edge a operar y es revocable de forma remota
(kill-switch anti-clon). Vive en `internal/lease` (`Issuer` lado servidor,
`Validator` lado Edge —este último modelado aquí; su cableado real en el daemon
es T6).

| Decisión (v1) | Detalle |
|---|---|
| Firma | **Ed25519**: el servidor firma; el Edge solo tiene la clave pública |
| Fuente de verdad | El **payload firmado** dentro de `LeaseUpdate.lease`. Los campos `expires_unix`/`revoked` solo lo **espejan** (inspección); el Validator nunca confía en ellos sin verificar la firma |
| Granularidad | **Por-Edge** (kill-switch de todo el Edge). `session_id` queda **reservado** para granularidad por-sesión futura (no implementado) |
| Gate 2-de-2 | `CanOperate = hasDEK ∧ leaseVigente` (firma válida ∧ no expirado ∧ no revocado). La DEK vive en el Edge; aquí se modela como booleano inyectado |
| Margen offline | Sin gracia extra: vale hasta `expires_unix`. El `Heartbeat.lease_counter` ancla la renovación; el Validator exige counter **estrictamente creciente** (anti-replay) |
| Revocación | **Pegajosa**: una vez revocado, ningún lease viejo —ni uno vigente posterior— des-revoca en v1 |

### Encoding del payload firmado

El blob de `LeaseUpdate.lease` es un sobre JSON `{claims, sig}` donde `claims`
son los bytes JSON **exactos** que se firman y `sig = ed25519.Sign(priv,
claims)`. Se firma y se verifica sobre **los mismos bytes embebidos** (no se
re-serializa antes de verificar), por lo que **no** se requiere un encoding
canónico/determinista. Es stdlib puro (`encoding/json` + `crypto/ed25519`), sin
dependencias nuevas. Se evitó un sub-mensaje proto a propósito: el `.proto` no
se toca en T5 y proto3 no garantiza serialización byte-estable.

### Emisión desde el servidor

`server.New()` se mantiene retrocompatible (opciones aditivas, como `WithEnroller`):

- `server.WithLeaseRenewal(issuer, ttl)` — al recibir un `Heartbeat` con
  `lease_counter=N`, el servidor emite un lease renovado (`counter=N+1`) válido
  por `ttl` y lo empuja por el mismo stream (best-effort).
- `server.PushLease(sessionID, *LeaseUpdate)` — empuja un lease arbitrario
  (renovación o **revocación**). El kill-switch se dispara con un `LeaseUpdate`
  revocado producido por `Issuer.Revoke`.

> El `edgeID` se deriva hoy del `session_id` observado (placeholder); la
> identidad real del Edge vendrá del cert mTLS en T6.

## Estado

**Greenfield.** Solo scaffold inicial. Ver `CLAUDE.md` para contexto
arquitectónico y `../../docs/piezas/02-cloudlink.md` para la especificación.

> El module path `github.com/wApp/wapp-cloudlink` es un placeholder ajustable
> al repositorio Git real cuando se publique.
