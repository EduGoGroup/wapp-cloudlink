# Constitución de `wapp-cloudlink`

> El documento que manda. Si algo de aquí choca con un comentario del código o con el `README.md`
> de la raíz del repo, **manda esto** — y corrige lo otro. El `README.md` de la raíz arrastra
> doce afirmaciones falsas verificadas, inventariadas en [`deuda.md`](deuda.md).

---

## 0. Lo que esta pieza es, en una frase

Un **módulo Go sin despliegue** que define el único canal entre el Edge (equipo del cliente) y la
nube: el `.proto`, su generado, y los paquetes que los dos extremos tienen que compartir para no
divergir. **Todo cambio aquí se paga en dos repos que no se compilan juntos.**

---

## 1. Invariantes del ecosistema que aplican a esta pieza

Se repiten aquí a propósito: este repo se clona solo y no tiene el resto del ecosistema al lado.

### INV-E1 · Zero-knowledge — la nube nunca ve credenciales ni llaves privadas

La nube **no accede** a la DEK, ni al store cifrado de `whatsmeow`, ni a las llaves Signal, ni a la
clave privada del Edge. Lo que **sí** sube a la nube, y a propósito, es el **contenido de negocio**:
el texto del mensaje entrante, el nombre visible del remitente, su número. Zero-knowledge protege
**llaves**, no contenido; confundir las dos cosas lleva a proponer que no suba el texto, que es
justo lo que la plataforma necesita para funcionar.

- **Cómo se comprueba aquí**: `grep -in "dek" proto/wapp/cloudlink/v1/cloudlink.proto` solo devuelve
  comentarios prohibitivos y **una métrica de tiempo**, `dek_load_duration_ms` (campo 4 de
  `SessionHealth`), que mide cuánto tardó en cargarse, no qué es. No existe ningún campo `bytes`
  que transporte material de llave del Edge.
- **Candado**: 🔴 **no hay ningún test que lo vigile en este repo.** Es una regla de revisión
  humana. Si añades un campo `bytes` nuevo, escribe en su comentario qué contiene y por qué no es
  material de llave.

### INV-E2 · Doble llave — DEK del cliente, Lease del servidor

Dos secretos con propósitos disjuntos, y **hacen falta los dos** para despachar (gate 2-de-2):

- **DEK**: descifra el store de `whatsmeow`. La custodia el **cliente**, en el keystore de su
  sistema operativo. **Jamás cruza este contrato.**
- **Lease**: autoriza a operar. Lo **emite y revoca el servidor**. Es el **kill-switch anti-clon**:
  quien robe una copia del store pero no reciba lease vigente, no puede despachar.

En este repo eso se materializa en el paquete `lease/`: `Issuer` (lado nube, custodia la privada
Ed25519) y `Validator` (lado Edge, solo la pública). La presencia de la DEK entra en el gate como
un **booleano inyectado**, nunca como material: `lease/validator.go:94` — `CanOperate(hasDEK bool)`
devuelve `hasDEK && v.leaseVigente()`.

- **Candado — el mejor del repo**: `lease/lease_test.go` con seis tests
  (`TestLeaseVigenteYGate2de2`, `TestRevocacionKillSwitch`, `TestLeaseExpirado`,
  `TestFirmaInvalida`, `TestAntiReplayCounter`, `TestE2ERevocacionPorStream`) más
  `lease/fuzz_test.go` (`FuzzOpen`).

### INV-E3 · Sin Redis ni broker en el Edge — la concurrencia se resuelve con Go

Ni aquí ni en el Edge hay cola externa. La durabilidad ante una caída del stream la da el **outbox
SQLite del Edge**, que reintenta al reconectar; por eso el servidor de referencia puede permitirse
**descartar** bajo saturación en vez de bloquear (`internal/server/cloudlink.go:69-98`). Un frame
perdido no es un mensaje perdido.

- **Cómo se comprueba**: las dependencias directas son **dos**, `go.mod:5-8`. No hay cliente de
  Redis, ni de AMQP, ni de NATS.

### INV-E4 · Copia-adaptación, nunca dependencia de EduGo

Parte del ecosistema wApp nació copiando y adaptando código de otro producto (**EduGo**) al espacio
de nombres de wApp. **Está prohibido importar un repo `edugo-*`.** Este repo no lo hace ni tiene por
qué: el lease firma con `crypto/ed25519` + `encoding/json` de la stdlib (`lease/lease.go:25-30`) y
la PKI con `crypto/x509`/`crypto/ecdsa`.

⚠️ **Ojo con el nombre del remoto**: el módulo se llama `github.com/EduGoGroup/wapp-cloudlink`. Eso
es la *organización* de GitHub, no una dependencia de EduGo. No lo leas como una violación.

- **Cómo se comprueba**: `grep -rn "edugo" --include='*.go' --include='*.mod' .` = 0 resultados.

### INV-E5 · El código compartido interno vive en `wapp-shared`, no aquí

`wapp-shared` es el monorepo multi-módulo propio de wApp (logger, config, auth, envelope, llm…) con
releases por módulo (tags `<modulo>/vX.Y.Z`). 🔴 **Este repo NO lo consume, y es deliberado**: un
contrato con dependencias arrastra a los dos extremos a resolverlas. Sus dos únicas dependencias
directas son gRPC y protobuf.

**No añadas `wapp-shared` a este `go.mod`** ni siquiera para un logger. Si necesitas loguear, es
que estás escribiendo en `cmd/` o en `internal/server/`, y ahí basta el `log` de la stdlib.

---

## 2. Invariantes propios de esta pieza

🔴 **Estos `INV-N` son LOCALES a `wapp-cloudlink` y no son los `INV-N` del ecosistema.** Son dos
numeraciones sin prefijo y **nadie arbitra entre ellas**, así que **tres identificadores están
tomados dos veces con significados distintos**:

| Aquí | En el corpus del ecosistema (otro significado) |
|---|---|
| **INV-8** · código de activación de un solo uso | «el tenant sobre el que se actúa sale del **token del llamante**» — tiene un ADR entero dedicado a su excepción (ADR-0039, «Plano de plataforma y excepción administrativa a INV-8») y lo citan así las constituciones de `wapp-platform-console` y `wapp-client-console` |
| **INV-10** · el cero de un enum de capacidad significa «no lo dice» | «los dos perímetros de autorización siguen aislados» (mapa de puertos del ecosistema y requisitos del Plan 047) |
| **INV-13** · las constantes compartidas viven en `transport/` | «`intake_jobs.source_text` se vacía en estado terminal» (ADR-0034 y el modelo de datos del cloud) |

**Consecuencia práctica: al citarlos fuera de este repo, escribe `INV-CL-8` o «INV-8 de
`wapp-cloudlink`»**, nunca `INV-8` a secas — un `grep` por `INV-8` devuelve las dos familias
mezcladas. Es exactamente el defecto que la nomenclatura del ecosistema prohíbe crear («si una
palabra ya está tomada en otro repo, no la reutilices: un grep es la herramienta con la que se
audita este proyecto»); queda **anotado, no resuelto**: renumerar rompería las citas ya escritas.


### INV-1 · El `.proto` es la fuente de verdad; `gen/` se commitea y solo lo escribe `buf generate`

`proto/wapp/cloudlink/v1/cloudlink.proto` es lo único que se edita a mano. El generado
(`gen/wapp/cloudlink/v1/cloudlink.pb.go`, 3.569 líneas, y `cloudlink_grpc.pb.go`, 233) **se
commitea a propósito**: el Edge lo importa cross-repo y sin él no habría contrato consumible.

- **Cómo se comprueba**: `make generate` (que corre `buf generate`) y después `git diff --exit-code
  gen/`. Si sale diff, alguien editó el generado a mano o el `.proto` no está regenerado.
- 🔴 **Nunca `protoc` directo.** La configuración vive en `buf.gen.yaml`: `paths=source_relative`,
  **sin managed mode** (el `go_package` va explícito en `cloudlink.proto:5`).
- **Candado**: ninguno automático. Es un paso de disciplina.

### INV-2 · Aditivo por defecto — y los cinco `reserved` no se tocan JAMÁS

Los cambios del contrato son **aditivos**: número de campo nuevo, sin renumerar, sin cambiar tipos.
Hay **cinco pares `reserved`** (número **y** nombre) y reutilizar cualquiera de ellos haría que un
Edge viejo interpretara otra cosa en ese hueco **sin producir un solo error**. La lista completa,
con fecha y motivo de cada retiro, está en [`contratos.md`](contratos.md).

- **Cómo se comprueba**: `grep -n reserved proto/wapp/cloudlink/v1/cloudlink.proto` debe devolver
  **exactamente diez líneas** (cinco pares). Si devuelve menos, alguien borró un `reserved`.
- **Candado parcial**: `gen/wapp/cloudlink/v1/cloudlink_contract_test.go:553`
  (`TestIncomingMessage_RetiredIntentFromOldEdge`) prueba que un emisor viejo que aún manda el
  campo 11 se decodifica sin romper. Los otros cuatro `reserved` **no tienen test**.
- 🔴 **Y aquí está el riesgo número uno del repo**: `buf.yaml` declara `breaking: use: [FILE]`,
  pero **ningún target del `Makefile` ni ningún workflow lo ejecuta**. `make ci-local` son cinco
  targets (`fmt-check vet lint-go test build`, `Makefile:42`) y **`buf lint` no es uno de ellos**.
  Romper el contrato aquí **no lo detiene nada**. Córrelos a mano; ver [`operacion.md`](operacion.md).

### INV-3 · Los paquetes públicos son públicos por contrato: `gen/`, `client/`, `lease/`, `mtls/`, `transport/`

Están en la **raíz**, no bajo `internal/`, y eso **no es un descuido**: los dos consumidores los
importan. Contado con `grep -rh 'EduGoGroup/wapp-cloudlink' --include='*.go'` sobre
`wapp-cloud-platform` y `wapp-edge-agent`: `gen/…v1` **101** · `lease` **13** · `mtls` **7** ·
`transport` **5** · `client` **4**. Ninguno es superficie muerta.

🔴 **Moverlos bajo `internal/` deja el ecosistema sin compilar.** La documentación vieja del repo
afirma tres veces que `lease` y `mtls` ya viven en `internal/`; es **falso**, y creerlo lleva a
«arreglarlo».

### INV-4 · `internal/server` NO se importa desde fuera, y el servidor de producción no está aquí

`internal/server/` es implementación de **referencia y demo** (`internal/server/server.go:5-13`).
El servidor CloudLink que terminan los Edges reales vive en `wapp-cloud-platform`, paquete
`internal/gateway/grpc`. Vive bajo `internal/` precisamente para que la barrera la imponga el
compilador.

- **Cómo se comprueba**: `grep -rn "wapp-cloudlink/internal" --include='*.go'` en los dos
  consumidores = **0 resultados** (verificado).

### INV-5 · El lease se valida sobre el payload FIRMADO, nunca sobre los campos top-level

`LeaseUpdate` lleva tres campos: `lease` (blob firmado), `expires_unix` y `revoked`. Los dos
últimos **son un espejo para inspección rápida**; la fuente de verdad es el blob. El `Validator`
verifica Ed25519 **antes** de mirar nada (`lease/validator.go:59-88`).

Además, el sobre firma y verifica sobre **los mismos bytes embebidos** (`signedLease.Claims`,
`lease/lease.go:69-72`): no se re-serializa antes de verificar, así que no hace falta un encoding
canónico y desaparece toda la clase de bugs «firmé A, verifiqué A' equivalente pero distinto byte
a byte». **No cambies eso a un sub-mensaje proto**: proto3 no garantiza serialización canónica
byte-estable entre versiones. El porqué está escrito en `lease/lease.go:56-68`.

- **Candado**: `lease/lease_test.go:117 TestFirmaInvalida` (estado intacto ante firma mala),
  `:146 TestAntiReplayCounter`, y `lease/fuzz_test.go:19 FuzzOpen`.

### INV-6 · La revocación es PEGAJOSA y no depende del counter

Un `LeaseUpdate` con `revoked=true` marca el estado revocado **para siempre** en ese `Validator`;
ningún lease posterior, por válido que sea, lo des-revoca (`lease/validator.go:71-79`). Y la
revocación **ignora el counter**: un kill-switch tiene que poder dispararse siempre
(`lease/issuer.go:64-76`).

- **Candado**: `lease/lease_test.go:64 TestRevocacionKillSwitch` y `:167 TestE2ERevocacionPorStream`.

### INV-7 · mTLS en `Connect`, TLS de solo servidor en `EnrollEdge` — y son dos puertas distintas

`Connect` exige cert de cliente: `mtls/mtls.go:24-31` fija `MinVersion: tls.VersionTLS13` y
`ClientAuth: tls.RequireAndVerifyClientCert`. `EnrollEdge` **no puede** exigirlo: el Edge todavía
no tiene certificado, y por eso esa puerta sirve **solo** el canje del código de activación
(`cloudlink.proto:7-12`). En producción son **dos listeners distintos** (`8102` enrolamiento,
`8101` Connect).

- **Cómo se comprueba**: `grep -n "RequireAndVerifyClientCert\|VersionTLS13" mtls/mtls.go`.
- **Candado**: `mtls/mtls_test.go:138 TestMTLSConnect` (aquí) y una batería mucho mayor en
  `wapp-cloud-platform` (`internal/gateway/grpc/mtls_test.go`, con el rechazo de cert ajeno).

### INV-8 · El código de activación es de UN SOLO USO, y la transición ocurre bajo el mismo lock

⚠️ **Identificador colisionado**: fuera de este repo, `INV-8` significa otra cosa. Ver el aviso de §2.

`MemoryStore.Consume` valida existencia, TTL y reuso **y marca usado bajo el mismo lock**, de forma
que dos consumos concurrentes del mismo código no pueden tener éxito los dos
(`internal/enroll/store.go:66-86`). En producción el store es Postgres y la condición va en el
propio `UPDATE`.

- **Candado**: `internal/enroll/enroll_test.go` — `TestEnrollInvalidCode`, `TestEnrollExpiredCode`,
  `TestEnrollUsedCode`, más `internal/enroll/fuzz_test.go:22 FuzzParseAndVerifyCSR`.

### INV-9 · Cero PII en los frames de telemetría

`MessageReceipt` transporta **solo** ids, estado y timestamp — nunca texto, número, JID ni
contenido (`cloudlink.proto:188-200`). `SessionHealth` transporta **solo** metadatos operativos
(`:259-265`). `DiagnosticsBundle` viaja **ya saneado y truncado en origen** (`:481-487`).

⚠️ La excepción declarada y deliberada es `IncomingMessage`, que **sí** lleva contenido de negocio
(`text`, `push_name`, `from_pn`, `from_lid`) — o sus versiones selladas en `enc_payload`. Eso es
INV-E1: sube contenido, no llaves.

- **Candado**: ninguno automático. Es regla de revisión.

### INV-10 · El cero de un enum de capacidad significa «no lo dice», JAMÁS un veredicto

⚠️ **Identificador colisionado**: fuera de este repo, `INV-10` significa otra cosa. Ver el aviso de §2.

`INFERENCE_READINESS_UNSPECIFIED` = «este Edge no lo dice» (un Edge viejo, o uno que aún no lo
sabe). **Nunca** «no puede servir». Mismo patrón en `SessionHealth.worker_taskset` (campo 9) e
`intent_circuit` (campo 5). El porqué está escrito en el `.proto` (`:250-256`): leerlo como DOWN
dejaría de calentar a toda la flota vieja **sin producir un solo error**, que es la forma más cara
de fallar.

- **Candado**: `gen/…/cloudlink_contract_test.go:232`
  (`TestHeartbeat_InferenceReadiness_OldSenderDecodesUnspecified`) y su simétrico forward en `:276`.

### INV-11 · `optional` cuando el cero es un valor pedible

`InferenceRequest.temperature` (5) y `max_output_tokens` (7) son `optional` porque **0 es un valor
que alguien puede pedir de verdad** y hay que distinguirlo de «no dije nada». Si añades un numérico
donde el cero sea legítimo, va `optional`.

- **Candado**: `gen/…/cloudlink_contract_test.go:442`
  (`TestInferenceRequest_TemperaturePresenceDistinguishesZeroFromUnset`).

### INV-12 · Ausencia ≠ cero, y no es lo mismo en campos vecinos

`InferenceLatency` ata el cuantil a su `samples` **por diseño** (`cloudlink.proto:404-413`), para
que sea imposible publicar un p50 sin su n. En `inference_prefill`/`inference_generation` (16, 17)
**la ausencia del sub-mensaje significa «no medible»**; en su vecino `intent_p50_ms` (10) es el
**cero** el que significa eso. Son convenciones distintas en campos contiguos: léelas, no las
supongas.

### INV-13 · Las constantes que los dos extremos deben saber igual viven en `transport/` y solo ahí

⚠️ **Identificador colisionado**: fuera de este repo, `INV-13` significa otra cosa. Ver el aviso de §2.

`transport.MaxMessageBytes` = **4 MiB** (`4 << 20`, `transport/limits.go:24`), aplicado en **los dos
sentidos y los dos extremos**. `transport.ControlSessionID` = **`"__wapp_control__"`**
(`transport/control_session.go:23`). El criterio de qué entra ahí está escrito en el propio
paquete: *lo que los dos extremos tienen que saber igual, y cuya divergencia no daría un error de
compilación sino un fallo de campo*. Duplicar cualquiera de las dos reintroduce el fallo en
silencio.

⚠️ `ControlSessionID` **NO es una sesión de WhatsApp**: es una ruta. La nube debe **registrarlo** en
su Registry (sin eso el login del operador no tiene por dónde volver) y **NO persistirlo** como
sesión de flota — si lo persiste, aparece una fila fantasma que el cliente ve en su panel como si
fuera un teléfono.

### INV-14 · `certs/` nunca entra en git

`.gitignore:19-23` excluye `certs/`, `*.key` y `*.pem`. Verificado: `git ls-files certs/` está
vacío aunque los ficheros existan en disco.

---

## 3. Tecnología y versiones reales

Sacadas de `go.mod` y del `Makefile`, no de memoria.

| Aspecto | Valor |
|---|---|
| Módulo | `github.com/EduGoGroup/wapp-cloudlink` (`go.mod:1`) |
| Go | **`go 1.26.5`** (`go.mod:3`; el mismo pin en `Makefile:4` y en `ci.yml:14`) |
| `google.golang.org/grpc` | **v1.82.1** (directa) |
| `google.golang.org/protobuf` | **v1.36.11** (directa) |
| Indirectas | `golang.org/x/net` v0.56.0 · `golang.org/x/sys` v0.46.0 · `golang.org/x/text` v0.39.0 · `google.golang.org/genproto/googleapis/rpc` |
| `golangci-lint` | **v2.12.2** (`Makefile:5`) |
| Generación | `buf` **v2** (`buf.yaml:1`, `buf.gen.yaml:1`) con `protoc-gen-go` + `protoc-gen-go-grpc` locales |
| Versión del generador empotrada | `protoc-gen-go v1.36.11` — **coincide con el runtime del `go.mod`** |
| Paquete proto | `wapp.cloudlink.v1` |
| Base de datos | **ninguna** |
| Frontend | **ninguno** |

🔴 El `README.md` de la raíz dice «Go 1.26.0». Es **falso**; son `1.26.5`.

---

## 4. Convenciones de código

- **Comentarios en español**, densos, explicando **el porqué**. El `.proto` es en su mayoría
  comentario de diseño: 783 líneas para ~40 declaraciones. Eso es intencional y es el activo
  principal del repo. Cuando añadas un campo, el comentario debe decir **qué significa su cero** y
  **quién lo puebla**.
- **Errores sentinela por paquete**, traducidos a códigos gRPC solo en la capa de transporte:
  `lease.ErrBadSignature|ErrStaleCounter|ErrMalformed`,
  `enroll.ErrCodeNotFound|ErrCodeExpired|ErrCodeUsed|ErrInvalidCSR`. El mapeo a `codes.*` vive en
  `internal/server/enrollment.go:41-53` y en ningún otro sitio.
- **Options funcionales** para la configuración: `server.New(opts...)`, `lease.NewIssuer(priv,
  opts...)`, `lease.NewValidator(pub, opts...)`. El reloj se **inyecta** (`WithIssuerClock`,
  `WithValidatorClock`) para tests deterministas: no llames `time.Now()` directo en código nuevo de
  `lease/`.
- **`stream.Send` se serializa siempre con un mutex** — no es seguro para uso concurrente. Está
  hecho en los dos lados: `internal/server/cloudlink.go:19-28` y `client/client.go:70-76`.
- **`GOWORK=off` en todos los targets del `Makefile`.** Si hay un `go.work` local (overlay de
  `wapp-shared` sin publicar), este repo lo ignora a propósito: su `go.mod` es la verdad.
- Nombres de servicio **de dominio, no genéricos**: `Enrollment` y `CloudLink`, sin sufijo
  `Service`; request/response del bidi son `EdgeToCloud`/`CloudToEdge`. Las tres reglas de `buf`
  que eso incumple están **exceptuadas y documentadas** en `buf.yaml`. No las «arregles».

---

## 5. Trampas conocidas

Las cosas que un agente hace mal aquí si nadie se lo dice.

### T1 · Creerse el `README.md` y el `CLAUDE.md` viejos de la raíz

Tienen **doce afirmaciones falsas verificadas** (inventario completo en [`deuda.md`](deuda.md)).
Las cuatro que más daño hacen:

- Dicen que `lease` y `mtls` viven en `internal/`. **Están en la raíz y son públicos** (INV-3).
- Dicen que el canal se autentica «con mTLS **+ token de plataforma**». **Ese token no existe** en
  el código ni en el contrato; la autenticación es mTLS y nada más. Los únicos «token» del repo son
  los del **usuario operador** (`access_token`/`refresh_token`), que son **carga relayada al IAM**,
  no autenticación del canal.
- Dedican una sección entera al umbral **`inline` vs `presigned_url`**. `inline` es `reserved 10`
  desde el 2026-08-12: **`SendMedia.src` tiene una sola rama** y no hay umbral que decidir. El
  comentario de `transport/limits.go:17-23` arrastra el mismo fantasma.
- Traen **dos números de versión caducados y contradictorios entre sí** (`v0.14.0` y `v0.10.0`)
  cuando el tag es `v0.17.0`.

### T2 · Consultar el último tag con `git tag | tail`

Ordena **lexicográficamente** y miente: `v0.15.0 < v0.9.0`. Con 18 tags te devuelve `v0.9.0`. Usa
`git for-each-ref --sort=-creatordate --format='%(refname:short)' refs/tags | head -1`.

### T3 · Dar por hecho que `session_id` y `command_id` son obligatorios

**No lo son.** proto3 no tiene `required`, y el propio contrato declara los casos donde el vacío es
correcto y **no es un error**: `InferenceRequest.session_id` va normalmente vacío (la inferencia es
del Edge, no de una sesión); en `ConfigUpdate` y `DiagnosticsRequest` el vacío significa «a todas
las sesiones / al Edge entero»; en los tres frames de auth «puede ir vacío». El `CLAUDE.md` viejo
afirma lo contrario.

### T4 · Añadir un campo y creer que el gate lo protege

No hay gate. `make ci-local` no corre `buf lint`, y **`buf breaking` no lo corre nadie**, aunque
`buf.yaml` lo tenga configurado. Un cambio incompatible pasa los cinco targets en verde. Córrelos a
mano antes de tocar el `.proto`.

### T5 · Creer que un PR valida algo

`ci.yml` es `on: workflow_dispatch` y nada más. El único workflow que se dispara solo es
`sync-main-to-dev.yml`, que **no valida nada**: hace fast-forward de `dev` tras un push a `main`.
Ver [`operacion.md`](operacion.md).

### T6 · Contar un `--- SKIP` como un `--- PASS`

Un `rc=0` de `go test` los cuenta igual. Aquí, además, hay una trampa específica: los dos `Fuzz*`
**solo corren su corpus semilla** en un `go test` normal. Sin `-fuzz` no fuzzean nada, y ningún
target del `Makefile` pasa ese flag.

### T7 · Tocar la implementación de referencia creyendo que es producción

`internal/server/` y `cmd/cloudlink/` son arneses. Arreglar ahí un bug de campo no arregla nada en
UAT: el servidor real es `wapp-cloud-platform`. Antes de invertir, pregunta qué proceso escucha
en `:8101` — es `bin/server` de la plataforma, no un binario de este repo. En el VPS de UAT **ni
siquiera hay checkout de `wapp-cloudlink`**: entra como módulo Go.

### T8 · Duplicar el keepalive «para que quede igual»

El bloque de keepalive (PING 30 s, Timeout 10 s, `MinTime` 15 s, `PermitWithoutStream`) está copiado
**tres veces**: `cmd/cloudlink/main.go:26-37`, `cmd/democloud/main.go:51-60` y el servidor real de
`wapp-cloud-platform`. Nada ata las tres copias y su divergencia **no da error de compilación**: da
un `GOAWAY too_many_pings` en campo. Si lo tocas, tócalas todas — o mejor, súbelo a `transport/`,
que es donde el propio paquete dice que debe vivir lo que los dos extremos deben saber igual.

### T9 · Escribir un `p50` sin su `n`

Prohibido por construcción en `InferenceLatency`, pero la trampa reaparece cada vez que alguien
quiere añadir «solo un cuantil más». Un p99 de una ventana con `n` pequeño es un **máximo
disfrazado**. Si añades un cuantil, ata su muestra al lado.

### T10 · Suponer que `enc_prompt` cifra algo hoy

`InferenceRequest.enc_prompt` (campo 9) está **previsto y hoy va vacío**: no existe par X25519 del
Edge, así que la nube no puede sellar hacia abajo. El prompt viaja **en claro dentro del mTLS**.
Está escrito en el `.proto`; no lo anuncies como cifrado.
