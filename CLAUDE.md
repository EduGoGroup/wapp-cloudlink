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

## Estructura del contrato gRPC (intención, no `.proto` final)

### Servicios

```
Enrollment.EnrollEdge (unario)
  → edge envía: código de un solo uso + CSR
  ← nube devuelve: certificado del Edge/tenant
  Transporte: TLS de servidor (aún sin mTLS, el Edge no tiene cert)

CloudLink.Connect (bidi-stream, mTLS)
  → edge abre una conexión persistente full-duplex
  Comandos cloud→edge: SendText, SendMedia, RunFlowStep, LeaseUpdate, Ping
  Eventos edge→cloud: IncomingMessage, DeliveryStatus, Ack, Heartbeat, Pong
```

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
internal/        → implementaciones Go (servidor gRPC lado cloud, cliente lado Edge)
cmd/cloudlink/   → entrypoint placeholder (puede ejecutarse standalone o embebido en wapp-cloud-platform)
```

El código generado de protobuf (`*.pb.go`, `*_grpc.pb.go`) puede vivir en
`internal/pb/` o en un subdirectorio de `proto/`; decidir antes de generar.

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
- Umbral exacto inline vs. URL prefirmada para media (ADR-0005).
- Esquema `.proto` final: nombres definitivos, versionado, compatibilidad ante actualizaciones del Edge (ADR-0011).

---

## Referencias

- Especificación: `../../docs/piezas/02-cloudlink.md`
- Edge Agent (cliente del stream): `../../docs/piezas/01-edge-agent.md`
- Plataforma Cloud (servidor del stream): `../../docs/piezas/03-plataforma-cloud.md`
- ADR-0006 (gRPC + mTLS + enrolamiento): `../../docs/adr/0006-cloudlink-grpc-mtls-enrolamiento.md`
- CLAUDE.md raíz: `../../CLAUDE.md`
