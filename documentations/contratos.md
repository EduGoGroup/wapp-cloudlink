# Contratos de `wapp-cloudlink`

> Todo lo que otros consumen de esta pieza. **De dónde sale cada lista**: la superficie gRPC se
> leyó entera de `proto/wapp/cloudlink/v1/cloudlink.proto` (783 líneas) y se contrastó contra
> `gen/wapp/cloudlink/v1/cloudlink_grpc.pb.go`. El recuento se hizo con
> `grep -c '^message ' / '^enum ' / '^service ' / '  rpc '` sobre el `.proto`.
>
> **No hay superficie HTTP, ni CLI con flags, ni base de datos.** Cero rutas, cero migraciones,
> cero versión de esquema en todo el repo.

**Recuento exacto: 2 servicios · 2 rpc · 28 mensajes · 6 enums · 5 pares `reserved`.**

| Dato del contrato | Valor |
|---|---|
| Paquete proto | `wapp.cloudlink.v1` (`cloudlink.proto:3`) |
| `go_package` | `github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1;cloudlinkv1` (`:5`) |
| Versión publicada | **`v0.17.0`** (2026-08-24) — **HEAD == ese tag** |
| Consumidores | `wapp-cloud-platform` y `wapp-edge-agent`, ambos pinados en `v0.17.0` |
| Importaciones externas del `.proto` | **ninguna** (ni siquiera `google.protobuf.*`) |

---

## 1. Servicio `Enrollment` — unario, TLS de solo servidor

El Edge todavía no tiene certificado, así que esta puerta **no puede** exigir cert de cliente. Por
eso sirve **una sola cosa**: el canje del código de activación. En producción vive en un listener
distinto del stream (`:8102` frente a `:8101`).

| rpc | Método gRPC completo |
|---|---|
| `EnrollEdge(EnrollEdgeRequest) → EnrollEdgeResponse` | `/wapp.cloudlink.v1.Enrollment/EnrollEdge` |

**`EnrollEdgeRequest`** (`cloudlink.proto:14-17`)

| # | Campo | Tipo | Qué es |
|---|---|---|---|
| 1 | `activation_code` | `string` | Código de **un solo uso** emitido por la plataforma |
| 2 | `csr_pem` | `bytes` | El CSR del Edge. La clave privada **nunca sale** del Edge |

**`EnrollEdgeResponse`** (`cloudlink.proto:19-32`)

| # | Campo | Tipo | Qué es |
|---|---|---|---|
| 1 | `edge_cert_pem` | `bytes` | El certificado hoja del Edge, firmado por la CA del tenant |
| 2 | `ca_chain_pem` | `bytes` | La cadena de la CA, para que el Edge valide al servidor |
| 3 | `tenant_id` | `string` | Empresa a la que queda atado el Edge |
| 4 | `cloud_enc_pubkey` | `bytes` | Pública **X25519** (32 B) de la nube, para que el Edge **selle** los campos sensibles hacia `enc_payload` |
| 5 | `lease_pubkey` | `bytes` | Pública **Ed25519** (32 B crudos, sin codificar) con la que se firma el lease. El Edge la persiste para validar **offline** cada lease y detectar un clon con DEK robada pero sin lease vigente. Añadida en `v0.12.0` |

🔴 **`lease_pubkey` no lo puebla nadie en este repo.** La interfaz `Enroller` devuelve cuatro
valores y no lo incluye (`internal/server/server.go:38-40`); `EnrollEdge` construye la respuesta con
cuatro campos (`internal/server/enrollment.go:58-63`); `Enrolled`, el cliente modelo del Edge,
tampoco lo expone (`internal/enroll/client.go:31-43`). Y **no hay ningún test** de ese campo, aunque
sí lo hay del vecino `cloud_enc_pubkey`. Es un campo del contrato **sin implementación de referencia
ni cobertura en su propio repo desde el 2026-08-13**. Ver [`deuda.md`](deuda.md).

**Mapeo de errores del servidor de referencia** (`internal/server/enrollment.go:41-53`), el único
sitio donde los errores sentinela se traducen a códigos gRPC:

| Condición | Código gRPC |
|---|---|
| `csr_pem` vacío · `enroll.ErrInvalidCSR` | `InvalidArgument` |
| `ErrCodeNotFound` · `ErrCodeExpired` · `ErrCodeUsed` | `PermissionDenied` (los tres iguales: no se filtra cuál) |
| Cualquier otro fallo | `Internal` |
| Sin `Enroller` inyectado | `Unimplemented` |

---

## 2. Servicio `CloudLink` — bidi-stream persistente, mTLS

| rpc | Método gRPC completo |
|---|---|
| `Connect(stream EdgeToCloud) → stream CloudToEdge` | `/wapp.cloudlink.v1.CloudLink/Connect` |

**Lo abre siempre el Edge** y se mantiene vivo 24/7. Los dos sobres llevan `command_id` = 1 y
`session_id` = 2 más un `oneof payload`. **Multiplexado por `session_id`, correlación por
`command_id`** (`cloudlink.proto:34-36`).

⚠️ **Ninguno de los dos campos es obligatorio.** proto3 no tiene `required` y el contrato declara
explícitamente los casos donde el vacío es correcto: `InferenceRequest.session_id` va normalmente
vacío; en `ConfigUpdate` y `DiagnosticsRequest` vacío significa «a todas las sesiones del Edge»; en
los tres frames de auth «puede ir vacío». El servidor de referencia acepta el vacío y entrega igual.

### 2.1 · Los 8 frames nube → Edge (`CloudToEdge.payload`)

| # | Frame | Qué hace | Línea |
|---|---|---|---|
| **10** | `SendText` | Ordena enviar un texto por WhatsApp. `to` = 1, `text` = 2. | `:106-109` |
| **11** | `SendMedia` | Ordena enviar media. `to`=1, `caption`=2, `mime`=3, `filename`=4, `kind`=5 (`MediaKind`, elige rama `DocumentMessage` vs `ImageMessage` — el mime no basta), `oneof src { presigned_url = 11 }`. El archivo **nunca viaja por el stream**: el Edge descarga la URL prefirmada de corta vida sin credenciales. | `:111-127` |
| **13** | `LeaseUpdate` | Renueva o **revoca** el lease. `lease`=1 (blob firmado), `expires_unix`=2, `revoked`=3. 🔴 Los dos últimos son un **espejo para inspección**; la fuente de verdad es el blob firmado. | `:137-141` |
| **14** | `Ping` | Prueba de vida de aplicación. `nonce`=1, que el `Pong` devuelve. | `:143-145` |
| **15** | `ConfigUpdate` | Empuja configuración al Edge sin desplegarlo. `command_id`=1, `session_id`=2 (**vacío = a todas**), `kind`=3 (hoy `"intents"`), `version`=4 (para idempotencia), `payload`=5 (bytes propios del `kind`). | `:457-467` |
| **16** | `DiagnosticsRequest` | Pide un volcado de diagnóstico. `command_id`=1, `session_id`=2 (**vacío = el Edge entero**), `scope`=3. Se responde con `DiagnosticsBundle`. | `:469-486` |
| **17** | `UserAuthResponse` | Respuesta **única** a los tres verbos de auth del operador. `command_id`=1, `session_id`=2 (eco), `oneof result { UserTokens tokens = 3 \| UserAuthError error = 4 }`. | `:756-764` |
| **18** | `InferenceRequest` | Pide al Edge que **sirva** una inferencia con su modelo local. El frame más cargado del contrato; detalle abajo. | `:523-654` |

**`InferenceRequest`, campo a campo** (10 campos):

| # | Campo | Tipo | Qué es |
|---|---|---|---|
| 1 | `command_id` | `string` | Correlación con el `InferenceResult` |
| 2 | `session_id` | `string` | **Normalmente vacío**: la inferencia es del Edge, no de una sesión. Vacío **no es un error** |
| 3 | `prompt` | `string` | Ya construido por la nube. El Edge lo pasa **verbatim**: no interpreta ni altera |
| 4 | `format` | `string` | Opaco: `"json"` o un JSON Schema |
| 5 | `temperature` | `optional float` | `optional` porque **0 es un valor pedible** y hay que distinguirlo de «no dije nada» |
| 6 | `timeout_ms` | `int64` | Plazo de **esta** petición |
| 7 | `max_output_tokens` | `optional int32` | Techo de salida. Ausente ⇒ default del Edge. `optional` por la misma razón que `temperature` |
| 8 | `class` | `string` | `"interactivo"` \| `"lote"`. 🔴 **SOLO telemetría; prohibido decidir con él** — no elige a quién servir, no entra en el aforo, no mueve el umbral del breaker. La prohibición está escrita en el `.proto` |
| 9 | `enc_prompt` | `bytes` | **Previsto y hoy VACÍO**: no existe par X25519 del Edge, así que la nube no puede sellar hacia abajo. El prompt viaja en claro **dentro del mTLS** |
| 10 | `warmup` | `bool` | Precalentamiento: la excluye del breaker, pero **sí ocupa plaza y aforo** |

El **modelo** y el flag `think` **no viajan**: son política del Edge, no del contrato
(`cloudlink.proto:513-521`).

### 2.2 · Los 10 frames Edge → nube (`EdgeToCloud.payload`)

| # | Frame | Qué hace | Línea |
|---|---|---|---|
| **10** | `IncomingMessage` | Un mensaje entrante de WhatsApp. `from`=1, `text`=2, `ts_unix`=3, `wa_message_id`=4, `is_group`=5, `push_name`=6, `from_pn`=7 (E.164 sin `+`), `from_lid`=8, `addressing_mode`=9 (`"pn"`\|`"lid"`), `enc_payload`=10. **Si `enc_payload` va, los planos sensibles viajan vacíos.** | `:147-171` |
| **12** | `Ack` | Acuse de un comando. `acked_command_id`=1, `ok`=2, `error`=3. | `:208-212` |
| **13** | `Heartbeat` | Liveness + estado. **Ancla la sesión** (el primero con identidad mTLS dispara `MarkOnline`) y **renueva el lease** por su `lease_counter`. Detalle abajo. | `:217-258` |
| **14** | `Pong` | Respuesta al `Ping`. `nonce`=1. | `:448-450` |
| **15** | `MessageReceipt` | Acuse de entrega/lectura de un saliente. `session_id`=1, `message_ids`=2 (repeated), `status`=3 (`ReceiptStatus`), `timestamp`=4, `command_id`=5. 🔴 **SOLO ids, estado y tiempo; nunca texto, número, JID ni contenido.** | `:193-200` |
| **16** | `DiagnosticsBundle` | Respuesta al `DiagnosticsRequest`. `command_id`=1, `log_tail`=2, `goroutine_dump`=3, `subsystems_json`=4. **Saneado y truncado en origen**; debe caber en los 4 MiB del transporte. | `:488-521` |
| **17** | `UserLoginRequest` | Login del **operador del Edge**, relayado al IAM de la nube. `command_id`=1, `session_id`=2, `email`=3, `password`=4. El tenant es **implícito del canal mTLS**: no viaja en el mensaje. | `:723-731` |
| **18** | `UserRefreshRequest` | `command_id`=1, `session_id`=2, `refresh_token`=3. | `:733-742` |
| **19** | `UserLogoutRequest` | `command_id`=1, `session_id`=2, `refresh_token`=3, `all_sessions`=4. | `:744-754` |
| **20** | `InferenceResult` | Resultado de la inferencia. `command_id`=1, `oneof result { bytes enc_output = 2 \| InferenceError error = 3 }`: **o la salida sellada, o un error nombrado, nunca las dos**. | `:656-678` |

**`Heartbeat`, campo a campo** (6 campos, `:217-258`):

| # | Campo | Qué es |
|---|---|---|
| 1 | `lease_counter` | Ancla de la renovación: la nube emite el siguiente con `counter+1` |
| 2 | `self_pn` | Número propio E.164 sin `+`. **Anti-self-loop**: la nube corta el bucle cuando el remitente es un número del propio tenant. Vacío en Edges antiguos |
| 3 | `self_jid` | JID crudo del device propio. **Solo trazabilidad**; la comparación se hace por `self_pn` |
| 4 | `state` | `SessionState`. 🔴 **Se llama `state`, NO `session_state`** — la documentación vieja del repo lo llama mal y `grep -rn session_state` sobre el `.proto` devuelve **cero** |
| 5 | `session_health` | `SessionHealth`, el snapshot operativo. **Opcional**: su ausencia es «sin datos de salud», no «salud mala» |
| 6 | `inference_readiness` | `InferenceReadiness`. **Estado, no transición**: va en TODOS los heartbeats. Añadido en `v0.17.0` |

**`SessionHealth`** (19 campos, `:266-402`) — el snapshot operativo, en tres bloques:

1. **Salud** (1-8): `whatsapp_socket_state`, `degraded_reason`, `last_inbound_event_age_s`,
   `dek_load_duration_ms` (⚠️ es una **métrica de tiempo**; no expone la DEK ni su material),
   `intent_circuit`, `outbox_depth`, `binary_version`, `daemon_uptime_s`.
2. **Worker / cajero** (9-15): `worker_taskset`, `intent_p50_ms`, `intent_omitted_by_reason`
   (`map<string,int64>`), `stuck_heads`, `stuck_head_polls`, `failed_seal_dispatch`,
   `failed_seal_budget`.
3. **Inferencia** (16-19): `inference_prefill` e `inference_generation` (ambos `InferenceLatency`),
   `inference_by_regime`, `inference_by_class` (mapas).

🔴 **Ausencia y cero no significan lo mismo en campos vecinos**: en `inference_prefill` /
`inference_generation` la **ausencia del sub-mensaje** significa «no medible»; en `intent_p50_ms`
(10) es el **cero** el que significa eso. Lee la convención de cada campo; no la supongas.

**Sub-mensajes**:

- **`SensitivePayload`** (`:173-191`): `text`=1, `push_name`=2, `from_pn`=3, `from_lid`=4. El Edge
  lo marshala y lo **sella con X25519** hacia `IncomingMessage.enc_payload`; la nube lo abre.
- **`InferenceOutput`** (`:680-701`): `raw_json`=1, **crudo y sin validar**. Va sellado dentro de
  `InferenceResult.enc_output`.
- **`InferenceLatency`** (`:404-413`): `p50_ms`=1, `samples`=2. **Ata el cuantil a su n por
  diseño**, para que sea imposible publicar uno sin el otro.
- **`UserTokens`** (`:769-778`): `access_token`, `refresh_token`, `token_type` (p. ej. `"Bearer"`),
  `expires_at`.
- **`UserAuthError`** (`:780-783`): `code` **es contrato** (estable); `message` **no lo es** (texto
  legible para humanos).

### 2.3 · Los 6 enums

| Enum | Valores | Línea |
|---|---|---|
| `MediaKind` | `UNSPECIFIED=0`, `DOCUMENT=1`, `IMAGE=2` | `:131-135` |
| `ReceiptStatus` | `UNSPECIFIED=0`, `DELIVERED=1` (✓✓), `READ=2` (✓✓ azul) | `:202-206` |
| `WhatsappSocketState` | `UNSPECIFIED=0`, `CONNECTED=1`, `CONNECTING=2`, `DEGRADED=3`, `DEAD=4` | `:415-423` |
| `SessionState` | `UNSPECIFIED=0` (heartbeat normal), `LOGGED_OUT=1` (WhatsApp cerró el device) | `:425-430` |
| `InferenceReadiness` | `UNSPECIFIED=0` (**«este Edge NO LO DICE»**, jamás DOWN), `READY=1`, `DOWN=2` | `:440-446` |
| `InferenceError` | `UNSPECIFIED=0`, `OLLAMA_DOWN=1`, `BREAKER_OPEN=2`, `TIMEOUT=3`, `LEASE_INVALID=4`, `EDGE_SIN_CAPACIDAD=5` | `:703-721` |

---

## 3. 🔴 Los 5 campos RETIRADOS — no reutilices su número ni su nombre

Un contrato se entiende tanto por lo que quitó como por lo que tiene. **Reutilizar cualquiera de
estos números haría que un Edge viejo interpretara otra cosa en ese hueco, sin producir un solo
error.** `grep -n reserved` sobre el `.proto` debe devolver **exactamente diez líneas**, cinco pares
(número **y** nombre).

| Mensaje | # | Nombre | Fecha | Motivo |
|---|---|---|---|---|
| `CloudToEdge` | **12** | `run_flow_step` | **2026-08-12** | Nació previsto y murió sin nacer: **nunca tuvo productor ni consumidor**. La máquina de estados vive entera en la nube y las respuestas salen como `SendText`/`SendMedia`, así que no había nada que este frame pudiera transportar. `:52-53` |
| `EdgeToCloud` | **11** | `delivery` | **2026-08-12** | El simétrico: **tenía consumidor y ningún productor**. La nube lo recibía (un `log.Debug` y nada más) y ningún punto del Edge lo emitió jamás. Los acuses reales son `MessageReceipt` (campo 15). `:83-84` |
| `SendMedia` | **10** | `inline` | **2026-08-12** | El contrato lo declaraba y **el Edge no lo miró nunca**: `handleSendMedia` lee siempre `presigned_url`, así que un `SendMedia` con `inline` producía una descarga vacía y un fallo. La media viaja por URL prefirmada de corta vida. `:122-123` |
| `IncomingMessage` | **11** | `intent` | **2026-08-24** | El push de la clasificación local, con el mensaje `ClassifiedIntent` **borrado entero**. Este **sí tenía productor** y aun así no entregaba: el Edge retenía cada entrante 4 s esperando una inferencia cuyo p50 real es 8,1 s, y de 430 inferencias **una** cupo en la ventana. La clasificación pasa de **push a pull**: la nube la pide con `inference_request`. `:167-168` |
| `SensitivePayload` | **5** | `intent` | **2026-08-24** | Era el camino «bueno» del push —la clasificación **sellada**— y muere con él. Bajo pull el Edge ya no clasifica por iniciativa propia. La salida del modelo viaja ahora en `InferenceResult.enc_output`. `:185-186` |

⚠️ **`SendMedia.src` tiene hoy UNA SOLA rama**, `presigned_url = 11`. No existe umbral «inline vs
URL» que decidir; la documentación vieja del repo dedica una sección entera a ese umbral y el
comentario de `transport/limits.go:17-23` lo repite. Es un fantasma.

---

## 4. Constantes de protocolo compartidas — paquete `transport`

Lo que los dos extremos tienen que saber **igual**, y cuya divergencia no daría error de
compilación sino un fallo de campo. Por eso vive aquí y no duplicado.

| Símbolo | Valor | Evidencia |
|---|---|---|
| `transport.MaxMessageBytes` | **4 MiB** (`4 << 20`) — aplicado en **los dos sentidos y los dos extremos**, y también a `EnrollEdge` | `transport/limits.go:24` |
| `transport.ServerOptions()` | `grpc.MaxRecvMsgSize` + `MaxSendMsgSize` para `grpc.NewServer` | `transport/limits.go:28-33` |
| `transport.DialOptions()` | El espejo, para `grpc.NewClient` | `transport/limits.go:37-43` |
| `transport.ControlSessionID` | **`"__wapp_control__"`** | `transport/control_session.go:23` |

**`ControlSessionID`, lo que hay que saber**: es el `session_id` que el Edge estampa en los frames
de auth (`UserLogin`/`Refresh`/`Logout`) **cuando todavía no hay ningún teléfono emparejado** — el
operador puede loguearse en el primer arranque. 🔴 **No es una sesión de WhatsApp: es una ruta.** La
nube debe **registrarlo** en su Registry (sin eso el login no tiene por dónde volver) y **NO
persistirlo** como sesión de flota: persistirlo crea una fila que el cliente ve en su panel como si
fuera un teléfono, con selector de perfil y como destino de envío.

---

## 5. API Go que exportan los paquetes públicos

### `lease/`

```go
lease.NewIssuer(priv ed25519.PrivateKey, opts ...IssuerOption) (*Issuer, error)
  (*Issuer).Issue(edgeID, tenantID string, ttl time.Duration, counter int64) (*cloudlinkv1.LeaseUpdate, error)
  (*Issuer).Revoke(edgeID, tenantID string) (*cloudlinkv1.LeaseUpdate, error)
  (*Issuer).PublicKey() ed25519.PublicKey
lease.NewValidator(pub ed25519.PublicKey, opts ...ValidatorOption) *Validator
  (*Validator).Apply(lu *cloudlinkv1.LeaseUpdate) error
  (*Validator).CanOperate(hasDEK bool) bool   // gate 2-de-2
  (*Validator).Revoked() bool
```

Opciones: `WithIssuerClock`, `WithValidatorClock` (inyección de reloj para tests deterministas).
Errores sentinela: `ErrBadSignature`, `ErrStaleCounter`, `ErrMalformed`.

### `mtls/`

```go
mtls.ServerCreds(serverCert tls.Certificate, clientCAs *x509.CertPool) credentials.TransportCredentials
mtls.ClientCreds(clientCert tls.Certificate, rootCAs *x509.CertPool, serverName string) credentials.TransportCredentials
mtls.LoadServerCredsFromFiles(certFile, keyFile, caFile string) (credentials.TransportCredentials, error)
mtls.LoadClientCredsFromFiles(certFile, keyFile, caFile, serverName string) (credentials.TransportCredentials, error)
```

`ServerCreds` fija `MinVersion: tls.VersionTLS13` y `ClientAuth: tls.RequireAndVerifyClientCert`.

### `client/`

```go
client.New(ctx context.Context, cc grpc.ClientConnInterface) (*Client, error)  // NO dialga
  (*Client).Send(msg *cloudlinkv1.EdgeToCloud) error   // serializado con mutex
  (*Client).Received() <-chan *cloudlinkv1.CloudToEdge // se cierra al terminar el stream
  (*Client).Err() error                                // consúmelo TRAS ver cerrado Received()
```

### `internal/server/` — ⚠️ inimportable desde otros repos, a propósito

`server.New(opts...)` con `WithEnroller`, `WithInboxCapacity`, `WithSaturationHook`,
`WithLeaseRenewal`; métodos `Push`, `PushLease`, `Received`, `Dropped`, `TotalDropped`.

---

## 6. Variables de entorno

Extraídas con `grep -rn 'os.Getenv\|envOr('`. **No hay loader con prefijo**: se leen crudas con
`os.Getenv`, así que **el nombre del código es el nombre efectivo** — a diferencia de otras piezas
del ecosistema, donde un `WithEnvPrefix("WAPP_")` compone el nombre real.

| Variable | Default | Quién la lee | Para qué |
|---|---|---|---|
| `CLOUDLINK_ADDR` | `:8101` | `cmd/cloudlink/main.go:46`, `cmd/democloud/main.go:42` | Dirección de escucha. **8101 es la banda wApp 81xx**, el mismo puerto del `Connect` de la plataforma |
| `CLOUDLINK_CERT_DIR` | `certs` | `cmd/cloudlink/main.go:47` | Directorio de la PKI de dev. 🔴 Si faltan `ca.crt`/`server.crt`/`server.key`, **arranca SIN mTLS** con solo un `log.Printf` |
| `CLOUDLINK_CMD_FILE` | *(sin default)* | `cmd/democloud/main.go:84` | Si va, `democloud` deja stdin y hace **tail-poll** de ese fichero cada 300 ms |
| `GOWORK` | forzada a `off` | `Makefile:22,25,28,33,39` | Ignora cualquier `go.work` local: el `go.mod` es la verdad |
| `EDGE_CN` | `edge-dev-001` | `scripts/gen-dev-certs.sh:21` | CN del cert de Edge de desarrollo |
| `DAYS` | `825` | `scripts/gen-dev-certs.sh:20` | Vigencia de los certs de desarrollo |

`.env.example` documenta **solo las dos primeras**. Las otras cuatro no están ahí.

---

## 7. Comandos

**No hay CLI con flags.** Los dos binarios se configuran solo por entorno. `democloud` acepta
**órdenes por stdin** (o por el fichero de `CLOUDLINK_CMD_FILE`), y son tres:

| Orden | Efecto |
|---|---|
| `send <sessionID> <destino> <texto...>` | Empuja un `SendText` a esa sesión |
| `ping <sessionID>` | Empuja un `Ping` |
| `quit` \| `exit` | Termina |

---

## 8. Ficheros que lee y escribe

**En producción, ninguno**: este módulo no se despliega.

| Ruta | Quién | Lectura / escritura |
|---|---|---|
| `certs/ca.crt`, `certs/server.crt`, `certs/server.key` | `cmd/cloudlink` | **Lee**. Si falta cualquiera, arranca sin mTLS |
| `certs/{ca,server,edge}.{crt,key}` | `scripts/gen-dev-certs.sh` | **Escribe** (y borra los `.csr` y `.ext` temporales). 🔒 `certs/` está en `.gitignore` |
| El fichero de `CLOUDLINK_CMD_FILE` | `cmd/democloud` | Lo **crea si no existe**, pero solo lo **lee**: se posiciona al final y sigue las líneas nuevas |
| `gen/wapp/cloudlink/v1/*.pb.go` | `buf generate` | **Escribe**. Se commitea; no lo edites a mano |

---

## 9. Tablas y esquemas que toca

**Ninguno.** Verificado: cero drivers SQL, cero ficheros `.sql`, cero carpeta de migraciones, cero
versión de esquema en todo el repo. Las dos únicas dependencias directas son gRPC y protobuf.

Lo único con estado es **en memoria y volátil**:

- `enroll.MemoryStore` — mapa de códigos de activación bajo mutex, con `GC()` de expirados y un
  `StartGC(ctx, every)` opcional. **En producción los códigos los emite y persiste la plataforma.**
- `cloudlinkService.sessions` y `.inboxes` — mapas por `session_id`
  (`internal/server/cloudlink.go:44-50`).
- El estado del `Validator` (aplicado / revocado / expiración / counter) es un struct con mutex:
  **no persiste**. Al reiniciar, el Edge vuelve a estado «sin lease aplicado».

La persistencia real de leases, flota y códigos vive en `wapp-cloud-platform`.
