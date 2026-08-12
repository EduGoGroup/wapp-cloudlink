# Changelog — wapp-cloudlink

El formato sigue [Keep a Changelog](https://keepachangelog.com/es-ES/1.0.0/)
y [Versionado Semantico](https://semver.org/lang/es/). Las versiones se cortan
como tags `vX.Y.Z` del contrato proto `wapp.cloudlink.v1`.

## [Unreleased]

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
