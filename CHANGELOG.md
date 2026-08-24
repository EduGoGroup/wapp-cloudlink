# Changelog — wapp-cloudlink

El formato sigue [Keep a Changelog](https://keepachangelog.com/es-ES/1.0.0/)
y [Versionado Semantico](https://semver.org/lang/es/). Las versiones se cortan
como tags `vX.Y.Z` del contrato proto `wapp.cloudlink.v1`.

## [Unreleased]

## [0.15.0] - 2026-08-24

### Added

**El par de frames de inferencia** (Plan 044 · Ola 1.6, ADR-0045, REQ-34): el Edge pasa
a comportarse como un **servidor de inferencia** para el Cloud.

- **`CloudToEdge.inference_request` (campo 18) → `InferenceRequest`.** El Cloud baja el
  prompt **ya construido** (`prompt`), el formato/JSON Schema esperado (`format`), la
  temperatura (`temperature`) y el presupuesto de tiempo (`timeout_ms`). El Edge no
  interpreta ni altera nada: *prompt entra → JSON sale*.
- **`EdgeToCloud.inference_result` (campo 20) → `InferenceResult`.** Correlacionado por
  `command_id` con el request (molde exacto de `DiagnosticsRequest`/`DiagnosticsBundle`).
  Su `oneof result` es o la salida sellada (`enc_output`) o un **error nombrado**
  (`InferenceError`), nunca las dos y nunca ninguna.
- **`InferenceOutput`**, sub-mensaje sellado (`envelope.SealFor`) con el `raw_json` crudo
  del modelo — análogo de `SensitivePayload`, y por la misma razón: sellar exige un
  mensaje que marshalar.
- **`enum InferenceError`**: `OLLAMA_DOWN`, `BREAKER_OPEN`, `TIMEOUT`, `LEASE_INVALID`,
  `EDGE_SIN_CAPACIDAD`. Enum y no string libre porque es un **vocabulario cerrado** que
  el consumidor mapea uno a uno a motivos de degradación; con string, un valor nuevo o
  mal escrito abajo se vuelve arriba un motivo desconocido que nadie nota.

**Tres decisiones que el `.proto` documenta y conviene no re-litigar:**

- **`think` NO es un campo.** Es política fija del Edge (`think:false` SIEMPRE): su único
  valor no-por-defecto degrada la máquina del cliente en órdenes de magnitud (medido: 4 s
  → 4 minutos), y además es vocabulario de Ollama, no de este contrato.
- **`temperature` es `optional`.** 0.0 es a la vez el valor que más se va a pedir
  (determinismo) y el cero del campo: sin presencia explícita, «quiero 0» y «no dije
  nada» serían el mismo byte en el cable.
- **El sellado es ASIMÉTRICO, y es un hecho del contrato, no un olvido.** Solo se
  distribuye `cloud_enc_pubkey`: el Edge sabe cerrar sobres hacia la nube y la nube
  abrirlos, pero el Edge no tiene par X25519 propio ⇒ **el resultado va sellado y la
  petición va en claro dentro del mTLS** (mismo criterio que el fallback del Plan 011
  §10.H). `InferenceRequest.enc_prompt` (campo 9) queda **previsto y vacío** para el día
  que el Edge tenga llave propia — algo que hoy no está en ninguna ola.

### Removed

⚠️ **CAMBIO QUE ROMPE — a propósito** (alpha, sin compatibilidad que preservar).

**Muere el push de la clasificación** (ADR-0045 §4, D-044.31): se retiran
`IncomingMessage.intent` (11) y `SensitivePayload.intent` (5), y el mensaje
`ClassifiedIntent` se **borra entero**. Los dos números y el nombre quedan `reserved`.

- **Por qué entero y no a plazos.** El push estaba muerto y **medido**: el Edge retenía
  cada entrante 4 s (`WAPP_AGENT_INTENT_WAIT_MS`) esperando una inferencia cuyo p50 real
  es 8,1 s — de **430 inferencias, UNA** cupo en la ventana, con descartes a 8 ms de
  llegar la etiqueta. Por estos dos campos **no llegó jamás un intent a la nube**.
- **Qué lo reemplaza.** El pull: el Cloud pide la clasificación con `inference_request`
  dentro de su ventana de agregación (45 s), donde sobra tiempo.
- **Compat durante el despliegue.** El proto se publica antes que el binario del Edge, así
  que habrá Edges viejos adjuntando todavía el campo 11: el Cloud nuevo lo parsea sin
  error y lo retiene como *unknown field* (test
  `TestIncomingMessage_RetiredIntentFromOldEdge`).
- `buf breaking` (regla FILE) contra `main` reporta **cuatro** hallazgos, todos de este
  retiro y ninguno de los frames nuevos. **No se apaciguan**; ningún target del Makefile
  ni del CI corre esa regla.


## [0.14.0] - 2026-08-21

### Added

**`transport.ControlSessionID`** — el `session_id` de control del stream, que hasta hoy
era un literal privado del Edge (MP-11).

- **Qué es.** El id fijo que el Edge estampa en los frames de autenticación
  (`UserLogin`, `UserRefresh`, `UserLogout`) cuando todavía no hay ninguna sesión de
  WhatsApp que pueda prestar el suyo: el gateway enruta la respuesta por
  `registry.Push(session_id)` y exige uno no vacío, pero el operador puede loguearse en
  el **primer arranque**, antes de emparejar ningún teléfono.
- **Por qué sube al contrato.** No es el Edge quien lo necesitaba compartido, sino la
  **nube** — y no para enrutarlo (eso ya lo hacía) sino para lo contrario: para **NO
  persistirlo** como sesión de flota. Sin eso nace una fila en `fleet_sessions` que no
  corresponde a ningún teléfono y que el cliente ve en su dashboard como si lo fuera.
- **Por qué aquí y no duplicado.** Su divergencia **no daría un error de compilación**:
  reaparecería la fila fantasma, en silencio. Es el criterio que este paquete ya aplicaba
  a los límites de transporte — lo que los dos extremos tienen que saber igual.

⚠️ **No toca el proto.** Es una constante Go del paquete `transport`; el contrato
`wapp.cloudlink.v1` queda intacto y la compatibilidad es total en ambos sentidos.

## [0.13.0] - 2026-08-17

### Added

Siete campos nuevos en **`SessionHealth`** (Plan 051 · T4.2), la telemetría que le
faltaba al camino de intents para poder operarse sin SSH. Cierran el camino de
**INV-051.3** (el desglose por motivo nunca agregado) y **REQ-051.17**.

- **`worker_taskset` (campo 9, `string`).** Veredicto del reparto de CPU entre el
  proceso del cajero y Ollama (T2.8): `"disjunta"` | `"solapada"` |
  `"cajero_sin_confinar"`. Vacío = este Edge no lo sabe.
- **`intent_p50_ms` (campo 10, `int64`).** p50 de la inferencia del clasificador
  local, en ms. `0` = no medible. No es el p50 del handler de whatsmeow.
- **`intent_omitted_by_reason` (campo 11, `map<string, int64>`).** Despachos sin
  intent publicado, desglosados por motivo (`fastlane`, `sin_texto`,
  `no_elegible`, `presupuesto`, `breaker`, `desconocido`, `apagado`,
  `fallo_repetido`). Mapa y no campos fijos: un motivo nuevo en el Edge no debe
  exigir un release de este contrato. Los contadores **nunca** se suman entre sí.
- **`stuck_heads` (campo 12, `int64`)**, **`stuck_head_polls` (campo 13,
  `int64`)**, **`failed_seal_dispatch` (campo 14, `int64`)** y
  **`failed_seal_budget` (campo 15, `int64`)**. Contadores del despachador nacidos
  en el barrido de la Ola 3 (T3.12). El 14 y el 15 van separados a propósito: solo
  uno de los dos implica mensajes duplicados.

**Cambio aditivo** — no rompe compatibilidad de wire. `SessionHealth` no tenía
ningún `reserved` y sus campos ocupados eran el 1..8, así que el 9..15 son huecos
nunca usados: un Edge o un Cloud viejo que no conoce estos campos simplemente los
ignora al deserializar, y uno nuevo los recibe en su cero (que en todos ellos
significa "sin dato", nunca "está bien"). Por eso `buf breaking` contra `main`
sigue en verde: no se renumera, no se renombra y no se cambia el tipo de nada.
El cambio es aditivo sobre `0.12.0`, así que la versión que lo publique será un
**minor**.

⚠️ **No se añadió ningún campo `ollama_ok`, y es deliberado — no lo "arregles"
después.** La sonda de Ollama se retiró a propósito en T3.0 del Plan 051 y hay un
test en el Edge que impide reintroducirla. La señal honesta de la salud de Ollama
es el estado del breaker del cajero, que ya tiene su campo desde el Plan 031:
**`intent_circuit` (campo 5)**. Lo que cambia con el Plan 051 es quién lo llena,
que es trabajo del Edge, no del contrato.

## [0.12.0] - 2026-08-13

### Added

- **`EnrollEdgeResponse.lease_pubkey` (campo 5, `bytes`).** La pública Ed25519
  (32B crudos) de la clave de firma del lease (kill-switch, ADR-0007), para que
  el enrolamiento se la entregue al Edge sin copiarla a mano. Mismo patrón que
  `cloud_enc_pubkey` (campo 4): reusa el mecanismo existente en vez de abrir un
  RPC nuevo. **Cambio aditivo** — no rompe compatibilidad de wire; un Edge
  viejo que no conoce el campo 5 simplemente lo ignora, y `buf breaking` contra
  `main` sigue en verde (Plan 055 · T4.1, D-055.5).

## [0.11.0] - 2026-08-12

### Removed

Limpieza de huérfanos del contrato (2026-08-12). Los tres campos que se retiran
**nunca transportaron un byte en producción**: se verificó, uno a uno, que no
tenían productor —y en dos casos, tampoco consumidor— en ninguno de los repos del
ecosistema. Los tres quedan `reserved` (número **y** nombre), que es la forma
correcta de retirar: el hueco no se reutiliza y ningún Edge viejo puede
interpretar otra cosa ahí. **El wire no cambia**: nadie los serializaba.

- **`CloudToEdge.run_flow_step` (campo 12) y su `message RunFlowStep`.** Sin
  productor NI consumidor desde que existe el contrato. La nube no lo emitía y el
  `switch` de `handleCommand` del Edge no lo contemplaba (caía en el `default`).
  La máquina de estados vive entera en la nube (ADR-0005) y las respuestas salen
  como `SendText`/`SendMedia`.
- **`EdgeToCloud.delivery` (campo 11) y su `message DeliveryStatus`.** El
  simétrico: consumidor sin productor. La nube lo recibía (un `log.Debug` en
  `connect.go`) pero ningún punto del Edge lo emitió jamás — los acuses reales
  viajan como `MessageReceipt` (campo 15) desde el Plan 013.
- **`SendMedia.inline` (campo 10 del `oneof src`).** El Edge nunca lo miró:
  `handleSendMedia` lee siempre `presigned_url`, así que un `SendMedia` con
  `inline` producía una descarga de URL vacía y un fallo. El media viaja por URL
  prefirmada de corta vida (Plan 017).

⚠️ **Para los consumidores**: es *breaking* a nivel de API Go (desaparecen
`EdgeToCloud_Delivery`, `SendMedia.GetInline()` y `RunFlowStep`), aunque
compatible a nivel de wire. `wapp-cloud-platform` y `wapp-edge-agent` ya vienen
ajustados en su rama de la misma limpieza; el orden al publicar es el de siempre:
**primero este contrato con su release, después los consumidores**.

## [0.10.1] - 2026-08-02

Parche de seguridad: solo dependencias. El contrato proto `wapp.cloudlink.v1` no
cambia — ni un campo, ni un número de tag —, así que cualquier consumidor de
`v0.10.0` sube sin tocar código. El código generado en `gen/` compila con el grpc
nuevo sin regenerarse.

### Security

- `google.golang.org/grpc` `v1.81.1` → `v1.82.1`: cierra **GO-2026-6061**, que
  cubre el motor RBAC de xDS y el transporte HTTP/2 del **servidor** gRPC. Pesa
  más aquí que en un módulo cualquiera: este repo define el contrato de CloudLink,
  el canal mTLS entre la nube y el Edge, y sus dos consumidores
  (`wapp-cloud-platform` y `wapp-edge-agent`) tienen la vulnerabilidad
  **alcanzable desde su propio código** — las trazas de `govulncheck` llegan hasta
  `bootstrap.serveGRPC` y `Adapter.send`. Este release es el primer eslabón para
  cerrarla; los consumidores la cierran al subir a esta versión.
- `golang.org/x/text` `v0.34.0` → `v0.39.0`: incluye **GO-2026-5970**, un bucle
  infinito ante entrada inválida.
- `golang.org/x/net` `v0.51.0` → `v0.56.0` y `golang.org/x/sys` `v0.42.0` →
  `v0.46.0` (esta última arrastrada por `go mod tidy`).
- `google.golang.org/genproto/googleapis/rpc` actualizado al snapshot que exige el
  grafo de módulos del grpc nuevo.

### Verificación

- `govulncheck ./...` tras el bump: **No vulnerabilities found**.
- `make ci-local` en verde con el toolchain fijado (Go 1.26.5 / golangci-lint
  v2.12.2): `gofmt`, `go vet`, lint sin hallazgos, `go test -race` y `go build`.

## [0.10.0] - 2026-07-16

Cambios aditivos y compatibles hacia atras con `v0.9.0`
(Plan 033, Ola 2 / ADR-0025 — autenticacion del operador del Edge relayada al IAM
por el stream bidi existente). El tenant es implicito del canal mTLS: NO viaja en
el mensaje. Modelo request/response correlacionado por `command_id` (patron
ConfigUpdate/Diagnostics). Verificado: `buf lint` OK, `buf breaking` (FILE) OK.

### Added

- Autenticacion de usuario del operador del Edge (Plan 033 / ADR-0025):
  - Nuevas peticiones en el oneof de `EdgeToCloud` (el Edge relaya credenciales
    hacia el IAM; nunca las custodia):
    - `EdgeToCloud.user_login = 17` → `UserLoginRequest { string command_id = 1;
      string session_id = 2; string email = 3; string password = 4; }`.
    - `EdgeToCloud.user_refresh = 18` → `UserRefreshRequest { string command_id = 1;
      string session_id = 2; string refresh_token = 3; }`.
    - `EdgeToCloud.user_logout = 19` → `UserLogoutRequest { string command_id = 1;
      string session_id = 2; string refresh_token = 3; bool all_sessions = 4; }`.
  - Nueva respuesta unica en el oneof de `CloudToEdge`, que sirve a las tres
    peticiones (correlacion por `command_id`):
    - `CloudToEdge.user_auth_response = 17` → `UserAuthResponse { string command_id
      = 1; string session_id = 2; oneof result { UserTokens tokens = 3;
      UserAuthError error = 4; } }`.
  - `UserTokens { string access_token = 1; string refresh_token = 2; string
    token_type = 3; int64 expires_at = 4; }` — espeja `domain.AuthResult` del IAM
    (AccessToken/RefreshToken/TokenType/ExpiresAt).
  - `UserAuthError { string code = 1; string message = 2; }` — `code` mapea los
    errores tipados del IAM (ErrInvalidCredentials/ErrUserInactive/ErrRefreshInvalid/
    ErrInvalidInput y tenant-cruzado); `message` es texto legible, no contrato.
  - Logout exitoso se modela con la rama `tokens` vacia (`UserTokens` sin campos)
    ⇒ ok sin credenciales nuevas; un fallo llega por la rama `error`.

## [0.9.0] - 2026-07-11

Cambios aditivos y compatibles hacia atrás con `v0.8.0` (Plan 031, Ola 0 —
telemetría de salud de flota + diagnóstico remoto, ADR-0023).

### Added

- Telemetría de salud de sesión adjunta al heartbeat (Plan 031 / ADR-0023):
  - Nuevo mensaje `SessionHealth { WhatsappSocketState whatsapp_socket_state = 1;
    string degraded_reason = 2; int64 last_inbound_event_age_s = 3; int64
    dek_load_duration_ms = 4; string intent_circuit = 5; int64 outbox_depth = 6;
    string binary_version = 7; int64 daemon_uptime_s = 8; }` — solo metadatos
    operativos; frontera zero-knowledge (ADR-0007): jamás llaves/DEK/credenciales.
  - Nuevo enum `WhatsappSocketState` (UNSPECIFIED/CONNECTED/CONNECTING/DEGRADED/
    DEAD): estado real del socket de WhatsApp con prueba de vida.
  - `Heartbeat.session_health = 5`: opcional; ausencia = "sin datos de salud"
    (Edge antiguo), no salud mala. Separa `link_state` (registro CloudLink) de la
    salud real del socket.
- Diagnóstico remoto bajo demanda (Plan 031 / ADR-0023):
  - Nuevo mensaje `DiagnosticsRequest { string command_id = 1; string session_id
    = 2; string scope = 3; }`.
  - Nuevo mensaje `DiagnosticsBundle { string command_id = 1; string log_tail = 2;
    string goroutine_dump = 3; string subsystems_json = 4; }` — el Edge sanea y
    trunca en origen; debe caber en el límite de 4 MiB del transporte.
  - `CloudToEdge.diagnostics_request = 16` y `EdgeToCloud.diagnostics_bundle = 16`
    (nuevas ramas de los oneof `payload`).

### Compatibilidad

- Todos los cambios son aditivos: campos/frames nuevos al final, sin renumerar.
  `buf breaking` (regla FILE) contra `main` pasa sin hallazgos. Tests de contrato
  bidireccionales verdes: un receptor de `v0.8.0` parsea `Heartbeat{SessionHealth}`
  y `CloudToEdge{DiagnosticsRequest}` sin error (campos nuevos retenidos como
  unknown fields); un emisor viejo decodifica en el shape nuevo con
  `session_health` nil.

## [0.8.0] - 2026-07-11

Cambios aditivos y compatibles hacia atrás con `v0.7.0` (Plan 029, Ola 0).

### Added

- Clasificador de intenciones local del Edge (Plan 029 / ADR-0020):
  - Nuevo mensaje `ClassifiedIntent { string intent = 1; map<string,string>
    params = 2; float confidence = 3; string config_version = 4; }`. El Cloud
    decide la precedencia; `params` puede llevar texto literal del cliente, por
    lo que viaja **preferentemente sellado**.
  - `SensitivePayload.intent = 5` (camino normal, dentro del sobre X25519).
  - `IncomingMessage.intent = 11`: espejo **en claro**, SOLO para despliegues
    sin sealed transit (mismo criterio que `text`/`push_name`).
- Push genérico de configuración Cloud→Edge (ADR-0021):
  - Nuevo mensaje `ConfigUpdate { string command_id = 1; string session_id = 2;
    string kind = 3; string version = 4; bytes payload = 5; }`.
  - `CloudToEdge.config_update = 15` (nueva rama del oneof `payload`). Primer
    `kind`: `"intents"`. Un Edge que no reconozca un `kind` debe ignorarlo.

### Compatibilidad

- Todos los cambios son aditivos: campos/frames nuevos al final, sin renumerar.
  `buf breaking` (regla FILE) contra `dev` pasa sin hallazgos. Un receptor de
  `v0.7.0` parsea un `CloudToEdge{ConfigUpdate}` sin error (oneof desconocido,
  frame retenido como unknown field).

## [0.7.0] y anteriores

Ver historial de tags para las versiones publicadas previas (`v0.1.0`–`v0.7.0`).
