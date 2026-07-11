# Changelog — wapp-cloudlink

El formato sigue [Keep a Changelog](https://keepachangelog.com/es-ES/1.0.0/)
y [Versionado Semantico](https://semver.org/lang/es/). Las versiones se cortan
como tags `vX.Y.Z` del contrato proto `wapp.cloudlink.v1`.

## [Unreleased]

Cambios aditivos y compatibles hacia atrás con `v0.8.0` (Plan 031, Ola 0 —
telemetría de salud de flota + diagnóstico remoto, ADR-0023). Sin release cortada
todavía.

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
