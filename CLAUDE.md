# CLAUDE.md — wapp-cloudlink (Pieza 02)

> Orientado a LLM. Lee esto antes de tocar cualquier archivo.
> Especificación completa: `../../docs/piezas/02-cloudlink.md`
> CLAUDE.md raíz del ecosistema: `../../CLAUDE.md` (si existe)

---

## Qué es esta pieza

**Contrato y conducto** entre el Edge Agent (lado del cliente) y la Plataforma
Cloud (lado del equipo wApp). Define el esquema protobuf (`.proto/`) y contiene
el servidor gRPC del lado cloud.

Es el **único canal** Edge↔cloud. **Nunca** viajan por aquí la DEK, el store
cifrado ni las llaves Signal; esos materiales se quedan solo en el Edge.

---

## Responsabilidad en wApp

| Qué hace CloudLink | Qué NO hace |
|---|---|
| Transporta órdenes de despacho (cloud→edge) | Contener lógica de negocio |
| Transporta eventos entrantes y estados (edge→cloud) | Custodiar la DEK |
| Transporta el lease operativo (renovación/revocación) | Encaminar mensajes de WhatsApp directamente |
| Autentica Edge/tenant con mTLS + token | Tomar decisiones de flujo |
| Enrolamiento por código de un solo uso (cert del Edge) | Guardar el store cifrado |
| Multiplexa N sesiones sobre un stream (por `session_id`) | Sustituir al broker/worker |

---

## Tecnología y decisiones clave (ADRs)

| ADR | Decisión | Impacto en código |
|---|---|---|
| ADR-0006 | gRPC bidi-stream + mTLS + enrolamiento por código único | Estructura del contrato protobuf; ciclo de vida del cert |
| ADR-0005 | Edge = despachador; payload completo armado por la nube | Los comandos en el stream llevan payload completo (texto, media, URL prefirmada) |
| ADR-0007 | Lease operativo revocable (kill-switch anti-clon) | `LeaseUpdate` en el stream; la DEK nunca viaja |
| ADR-0008 | Multi-teléfono: N sesiones por Edge, un solo stream | Todos los mensajes del stream llevan `session_id` |
| ADR-0003 | Sin broker en el Edge; `outbox` SQLite | El stream puede interrumpirse; el Edge encola y drena al reconectar |
| ADR-0011 | Auto-actualización firmada | El contrato protobuf debe versionar para compatibilidad con Edges desactualizados |

---

## Estructura del contrato gRPC (`wapp.cloudlink.v1`, tag actual `v0.9.0`)

> El `.proto` **existe y está en vivo** (`proto/wapp/cloudlink/v1/cloudlink.proto`); el
> código generado se commitea en `gen/` (ver README §«Código generado»). Los cambios se
> cortan como tags `vX.Y.Z` y son **aditivos** (`buf breaking` contra `main`).

### Servicios

```
Enrollment.EnrollEdge (unario)
  → edge envía: código de un solo uso + CSR
  ← nube devuelve: certificado del Edge/tenant
  Transporte: TLS de servidor (aún sin mTLS, el Edge no tiene cert)

CloudLink.Connect (bidi-stream, mTLS)
  → edge abre una conexión persistente full-duplex
  Comandos cloud→edge (oneof): SendText, SendMedia, RunFlowStep, LeaseUpdate, Ping,
                               ConfigUpdate (ADR-0021), DiagnosticsRequest (ADR-0023)
  Eventos edge→cloud (oneof):  IncomingMessage, DeliveryStatus, Ack, Heartbeat, Pong,
                               MessageReceipt, DiagnosticsBundle (ADR-0023)
```

`Heartbeat` adjunta `SessionHealth` (snapshot operativo por sesión, solo metadatos:
socket, degradación, edad del último entrante, outbox, `binary_version`, uptime) más
`self_pn`/`self_jid` (anti-self-loop) y `session_state`. `IncomingMessage` puede sellar
los campos sensibles en `SensitivePayload` (X25519), incluido el `ClassifiedIntent` del
clasificador local del Edge (ADR-0020). Detalle completo de frames y enums en el README.

### Campos obligatorios en todos los mensajes del stream

- `session_id` — identifica la sesión/teléfono dentro del Edge (multiplexado).
- `command_id` — correlaciona comando↔ack de forma asíncrona.

### Frontera de seguridad (dura)

| Viaja por CloudLink | NUNCA viaja |
|---|---|
| Texto, metadatos de media | DEK (clave que descifra el store) |
| URLs prefirmadas de corta vida | Store cifrado (`msg_enc_*`, el `.db`) |
| Eventos entrantes (contenido de negocio) | Llaves Signal, llaves X25519 |
| Lease operativo firmado | Material de pairing de whatsmeow |

---

## Layout del repositorio

```
proto/           → archivos .proto (fuente de verdad del contrato)
gen/             → código protobuf/gRPC generado con buf (SE COMMITEA; importable cross-repo)
client/          → cliente Go del lado Edge (exportado)
internal/        → server (implementación de REFERENCIA/demo, no la de producción), lease, mtls
cmd/cloudlink/   → arnés/entrypoint standalone para validar el contrato e2e; cmd/democloud (demo)
```

> El servidor CloudLink de **producción** que terminan los Edges vive en
> `wapp-cloud-platform` (`internal/gateway/grpc`), no aquí: `internal/server` es solo
> referencia legible e insumo de los arneses e2e (ver README §«Frontera servidor»). El
> código generado se regenera con `buf generate` (nunca `protoc` directo) y vive en `gen/`,
> jamás bajo `internal/` (los Edges deben importarlo cross-repo).

---

## Ciclos de vida clave

1. **Enrolamiento**: Edge genera par de claves + CSR → `EnrollEdge` → recibe cert → todo lo demás va por `Connect` con mTLS.
2. **Stream Connect**: Edge inicia, autenticación mTLS, stream persistente full-duplex. La nube empuja órdenes; el Edge emite eventos y heartbeats.
3. **Lease**: se renueva por heartbeat; si se revoca, el Edge recibe `LeaseUpdate(REVOCADO)` y el store se bloquea (kill-switch).
4. **Resiliencia**: si el stream cae, el Edge reintenta con backoff exponencial + jitter; mientras tanto encola en `outbox` SQLite.

---

## Puntos abiertos (no implementar sin consenso)

- PKI: vida del cert del Edge, renovación automática, propagación de revocación (ADR-0006).
- TTL exacto del código de activación y reenrolamiento si el Edge pierde su credencial.
- Cadencia de keep-alive (Ping/Pong) y detección de stream zombi.
- Umbral inline vs. URL prefirmada para media: **resuelto** — `transport.MaxMessageBytes`
  (4 MiB) como fuente única; media que lo exceda **debe** viajar `presigned_url` (ver README).
- Granularidad del lease **por-sesión**: `session_id` está reservado en el contrato pero el
  kill-switch hoy es **por-Edge** (no implementada la revocación por sesión).

---

## Referencias

- Especificación: `../../docs/piezas/02-cloudlink.md`
- Edge Agent (cliente del stream): `../../docs/piezas/01-edge-agent.md`
- Plataforma Cloud (servidor del stream): `../../docs/piezas/03-plataforma-cloud.md`
- ADR-0006 (gRPC + mTLS + enrolamiento): `../../docs/adr/0006-cloudlink-grpc-mtls-enrolamiento.md`
- CLAUDE.md raíz: `../../CLAUDE.md`
