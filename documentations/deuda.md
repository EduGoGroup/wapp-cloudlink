# Deuda viva de `wapp-cloudlink`

> Todo lo de aquí se verificó contra el árbol de trabajo en `b52e985` (= `v0.17.0`) el
> **2026-08-30**. Lo que no se pudo verificar va marcado **NO VERIFICADO**.
>
> Contexto que cambia la severidad: **este módulo no se despliega**. Los defectos que viven en
> `internal/server/` y en `cmd/` afectan a arneses de desarrollo, no a producción — el servidor
> real es `wapp-cloud-platform`. Aun así están aquí, porque `cmd/cloudlink` **está pensado para
> correr** y porque `internal/server` es la referencia legible que otros copian.

---

## 0. Resumen por severidad

| # | Deuda | Dónde | Severidad |
|---|---|---|---|
| D-1 | `buf lint` fuera del agregado y `buf breaking` fuera de todo | `Makefile:42` | 🔴 **alta** — es un repo que ES un contrato |
| D-2 | `lease_pubkey` declarado sin implementación ni test en su propio repo | `internal/server/enrollment.go:58-63` | 🔴 alta |
| D-3 | Fuga de mapa: `inboxes` nunca se limpia | `internal/server/cloudlink.go:225-233` | 🟠 media |
| D-4 | Fuga de goroutines: el canal del inbox nunca se cierra | `internal/server/cloudlink.go:122-126` | 🟠 media |
| D-5 | Inbox compartido bajo la clave vacía | `internal/server/cloudlink.go:169-177` | 🟠 media |
| D-6 | El `README.md` y el `CLAUDE.md` de la raíz tienen **12 afirmaciones falsas** | ver §2 | 🔴 alta (envenena a todo agente que entra) |
| D-7 | Comentario que documenta un campo **retirado** en la fuente única del transporte | `transport/limits.go:17-23` | 🟠 media |
| D-8 | El `edgeID` del lease es un **placeholder**: es el `session_id` y el tenant va vacío | `internal/server/cloudlink.go:195` | 🟠 media |
| D-9 | Degradación silenciosa a sin-TLS | `cmd/cloudlink/main.go:53-62` | 🟠 media |
| D-10 | Keepalive triplicado, sin nada que ate las copias | `cmd/cloudlink/main.go:26-37` | 🟡 baja |
| D-11 | El cliente del Edge no tiene backpressure, y la asimetría no está escrita | `client/client.go:55` | 🟡 baja |
| D-12 | `golangci-lint` sin fichero de configuración | raíz del repo | 🟡 baja |
| D-13 | `make tools` instala `buf@latest` sin pinar | `Makefile:9` | 🟡 baja |
| D-14 | Dos errores tragados a la vez en la renovación del lease | `internal/server/cloudlink.go:196-202` | 🟡 baja (documentado) |

**Código muerto verificado: NO HAY.** Los cinco paquetes públicos se importan realmente desde los
consumidores (`gen` 101 · `lease` 13 · `mtls` 7 · `transport` 5 · `client` 4). Ningún símbolo
exportado quedó huérfano.

---

## 1. La deuda, una por una

### D-1 · 🔴 Ningún gate vigila la compatibilidad del contrato

**Dónde**: `Makefile:42` (`ci-local: fmt-check vet lint-go test build` — cinco targets, y `lint` no
es uno) · `buf.yaml` (que **sí** declara `breaking: use: [FILE]`).

**Consecuencia**: para un repo cuyo producto **es** un contrato, el invariante «aditivo por
defecto» depende de que alguien se acuerde de correrlo a mano. Un cambio incompatible —renumerar un
campo, cambiar un tipo, reutilizar un `reserved`— **pasa los cinco gates en verde** y llega a un tag.
El daño no se vería en compilación sino en campo, con un Edge viejo interpretando otra cosa.

**Cómo se cierra**: añadir `lint` al agregado y un target `breaking` que corra
`buf breaking --against '.git#branch=main'`, e incluirlo también. Coste: dos líneas de `Makefile`.
El único matiz real es que a veces se rompe **a propósito** (ya pasó el 2026-08-24 con la retirada
del `intent`), así que el target debe poder saltarse con una variable **explícita**, nunca por
omisión.

### D-2 · 🔴 `lease_pubkey` es un campo del contrato sin implementación ni cobertura aquí

**Dónde**: declarado en `proto/wapp/cloudlink/v1/cloudlink.proto:31` desde `v0.12.0` (2026-08-13),
pero:

- `internal/server/server.go:38-40` — la interfaz `Enroller` devuelve **cuatro** valores y no lo
  incluye.
- `internal/server/enrollment.go:58-63` — `EnrollEdge` construye la respuesta con **cuatro** campos.
- `internal/enroll/client.go:31-43` — `Enrolled`, el cliente modelo del Edge, tampoco lo expone.
- **No hay ningún test** de ese campo, aunque sí lo hay de su gemelo `cloud_enc_pubkey`
  (`internal/enroll/enroll_test.go:134 TestEnrollCloudEncPubkey`).

**Consecuencia**: la implementación de referencia **no sirve de referencia** para el campo con el
que el Edge valida el kill-switch offline. Quien copie de aquí lo omitirá. Y no hay red que avise.
🔴 **NO VERIFICADO**: si `wapp-cloud-platform` lo puebla de verdad no se comprobó desde este repo.

**Cómo se cierra**: extender `Enroller` a cinco valores de retorno, propagarlo en `EnrollEdge` y en
`Enrolled`, y escribir el hermano de `TestEnrollCloudEncPubkey`. Es media hora.

### D-3 · Fuga de memoria: el mapa `inboxes` crece para siempre

**Dónde**: `internal/server/cloudlink.go:225-233` (`deregister`) borra de `s.sessions` pero **nunca**
de `s.inboxes`. `grep -n 'delete(' internal/server/*.go` devuelve **una sola línea**:
`delete(s.sessions, sid)`.

**Consecuencia**: en un proceso 24/7 el mapa crece sin tope — una entrada, más su canal de 64
punteros, **por cada `session_id` visto jamás**, aunque su stream haya muerto hace semanas.

**Cómo se cierra**: borrar también el inbox en `deregister`, con cuidado de no matar el de una
reconexión más nueva — la misma comprobación `if s.sessions[sid] == c` que ya se hace. Y cerrar el
canal antes de soltarlo (ver D-4), que es la mitad no trivial.

### D-4 · Fuga de goroutines: el canal del inbox nunca se cierra

**Dónde**: `internal/server/cloudlink.go:122-126` — `forward` es `for msg := range ib.ch`, y
`grep -n 'close(' internal/server/*.go` **no devuelve ninguna línea**.

**Consecuencia**: una vez llamado `Received()`, queda **una goroutine viva para siempre por cada
sesión**. Se acumulan con D-3.

**Cómo se cierra**: junto con D-3. Cerrar `ib.ch` al dar de baja la sesión termina el `range` y la
goroutine. Ojo con el orden: hay que garantizar que ningún `deliver` escriba en un canal ya cerrado
— probablemente marcando el inbox como cerrado bajo el mismo `s.mu`.

### D-5 · Todos los frames sin `session_id` caen en un inbox compartido

**Dónde**: `internal/server/cloudlink.go:169-177`. `Connect` filtra `sid != ""` para **registrar** y
para **renovar el lease**, pero llama a `s.deliver(sid, msg)` **fuera** de ese `if`.

**Consecuencia**: los frames sin sesión de **todos** los Edges comparten un mismo inbox de 64 bajo
la clave `""`. Un Edge ruidoso descarta los frames sin sesión de los demás — justo lo que el
aislamiento por sesión venía a evitar. Y no es un caso raro: el contrato declara que el vacío es
correcto en `InferenceRequest`, en los frames de auth y en los de alcance-Edge.

**Cómo se cierra**: la decisión de diseño primero — o se enruta el vacío al inbox del `conn` (no del
`session_id`), o se le da capacidad propia por conexión. Meterlo en el `if` sin más **perdería** los
frames sin sesión, que es peor.

### D-6 · Los `.md` de la raíz del repo mienten en doce puntos

Ver el inventario completo en §2. **Consecuencia**: es la deuda que más caro sale, porque envenena a
todo agente que entra al repo antes de mirar el código. Ya provocó al menos una confusión de
primer orden: creer que `lease` y `mtls` viven bajo `internal/`.

**Cómo se cierra**: el `README.md` de la raíz se reescribe o se recorta a un puntero hacia
`documentations/`. El `CLAUDE.md` **ya se reescribió** en este mismo pase.

### D-7 · El fichero que existe para que los extremos no diverjan describe un frame que ya no existe

**Dónde**: `transport/limits.go:17-23` — «SendMedia lleva la carga como `inline` (bytes) o como
`presigned_url`… un `inline` grande bloquearía…». `inline` es **`reserved 10` desde el 2026-08-12**
(`cloudlink.proto:122-123`) y `SendMedia.src` tiene **una sola** rama.

**Consecuencia**: la fuente única de verdad del transporte documenta una elección que no existe.
Quien la lea implementará una rama muerta.

**Cómo se cierra**: reescribir el comentario. Es literalmente eso; el código está bien.

### D-8 · El `edgeID` del lease es un placeholder: es el `session_id`, y el tenant va vacío

**Dónde**: `internal/server/server.go:84-86` lo declara («placeholder; la identidad real del Edge
vendrá del cert mTLS más adelante») y `internal/server/cloudlink.go:195` lo materializa:
`Issue(sessionID, "", s.leaseTTL, hb.GetLeaseCounter()+1)`.

**Consecuencia**: en un kill-switch por-Edge, el claim `edge_id` del lease firmado **no identifica
al Edge sino a una de sus sesiones**, y el `tenant_id` firmado va vacío. En la referencia. 🔴 **NO
VERIFICADO** cómo lo resuelve el servidor real de `wapp-cloud-platform`.

**Cómo se cierra**: derivar la identidad del **CN del certificado mTLS** del stream, que es donde ya
vive en producción. Requiere que el servidor de referencia lea el peer del contexto gRPC.

### D-9 · Degradación silenciosa a sin-TLS

**Dónde**: `cmd/cloudlink/main.go:53-62`. Si falta **cualquiera** de `ca.crt`, `server.crt` o
`server.key`, arranca **sin mTLS** con un `log.Printf`, no un fallo.

**Consecuencia**: un `CLOUDLINK_CERT_DIR` mal escrito levanta un CloudLink abierto y nada lo impide.
Solo el nombre del binario (arnés de dev) lo excusa — y aun así es el único binario del repo pensado
para correr de verdad.

**Cómo se cierra**: una variable explícita (`CLOUDLINK_ALLOW_INSECURE=1`) y `log.Fatalf` en su
ausencia. Postura de partida **cerrada**, con escape declarado.

### D-10 · El keepalive está copiado tres veces y nada ata las copias

**Dónde**: `cmd/cloudlink/main.go:26-37` y `cmd/democloud/main.go:51-60`, más el servidor real de
`wapp-cloud-platform` — que los comentarios de los dos ficheros declaran que debe ser **idéntico**.
🔴 **NO VERIFICADO** contra ese tercer repo. La función `envOr` también está duplicada
(`cmd/cloudlink/main.go:81-86` ↔ `cmd/democloud/main.go:220-225`).

**Consecuencia**: su divergencia **no da error de compilación**: da un `GOAWAY too_many_pings` en
campo, que es de los fallos más caros de diagnosticar.

**Cómo se cierra**: subir el keepalive a `transport/`, que es literalmente donde el propio paquete
dice que debe vivir lo que los dos extremos tienen que saber igual y cuya divergencia no compila mal.
Eso reduciría tres copias a una **y** cerraría la parte que hoy vive en otro repo.

### D-11 · El cliente del Edge no tiene backpressure, y la asimetría no está escrita

**Dónde**: `client/client.go:55` — `recvLoop` hace `c.received <- msg` **bloqueante** sobre un canal
de 64, sin `select`/`default`. Contrasta con `deliver` del servidor, que sí descarta.

**Consecuencia**: si el consumidor del Edge se atasca, el `Recv` del stream **se congela entero** y
el servidor no se entera. Puede ser deliberado —el Edge no debe perder comandos— pero **no está
escrito en ninguna parte**, así que el próximo lector lo leerá como un descuido y lo «arreglará».

**Cómo se cierra**: con un comentario, si es deliberado. Si no lo es, hace falta decidir qué se
hace: descartar aquí sería perder un `SendText`.

### D-12 · `golangci-lint` sin configuración

**Dónde**: `find . -name '.golangci*'` no devuelve nada.

**Consecuencia**: `make lint-go` corre con los linters **por defecto de la versión instalada**. Qué
se comprueba depende de tu máquina, no del repo — y el resultado puede diferir entre `ci-local` y
`ci-docker` sin que nadie sepa por qué.

**Cómo se cierra**: un `.golangci.yml` con la lista explícita, aunque sea la de por defecto.

### D-13 · La generación del contrato es el paso menos reproducible

**Dónde**: `Makefile:9` — `go install github.com/bufbuild/buf/cmd/buf@latest`, mientras Go
(`Makefile:4`) y golangci-lint (`:5`) sí están pinados.

**Consecuencia**: lo único que este repo **produce** se genera con una herramienta sin versión fija.
Dos máquinas pueden emitir `gen/` distinto.

**Cómo se cierra**: pinar `buf` a una versión concreta, igual que los otros dos.

### D-14 · Dos errores tragados a la vez en la renovación del lease

**Dónde**: `internal/server/cloudlink.go:196-202` — el de `Issue` (`return` mudo) y el del envío
(`_ = c.send(...)`).

**Consecuencia**: está **documentado** como best-effort deliberado (`:181-186`) y el argumento es
bueno —el lease vigente sigue valiendo hasta expirar y el Edge reintentará al siguiente heartbeat—
pero deja el push del kill-switch **sin ninguna señal de que falló**.

**Cómo se cierra**: un contador o un hook, como el de saturación. No hace falta cambiar la política.

---

## 2. Inventario de afirmaciones falsas en los `.md` de la raíz

Verificadas una por una contra el código. **No heredes ninguna.** El `CHANGELOG.md`, en cambio,
salió **limpio**: cada entrada contrastada existe en el `.proto` y en el `.pb.go`.

| # | Dice | Realidad |
|---|---|---|
| C1 | `README.md:63` — «último tag **`v0.14.0`**» | Es **`v0.17.0`**, tres versiones después |
| C2 | `README.md:208-209` — «última entrada del CHANGELOG: **`v0.10.0`**» | v0.10.0 es de 2026-07-16; hay **siete** versiones posteriores. Y el mismo README **se contradice consigo mismo** (C1 dice 0.14.0) |
| C3 | `README.md:19` y `:167` — el lease vive en **`internal/lease`**; `:146` — las credentials en **`internal/mtls`** | Ambos están en la **RAÍZ** y son **públicos**: los consumidores los importan 13 y 7 veces. Bajo `internal/` **el ecosistema no compilaría** |
| C4 | `CLAUDE.md:128` — «`internal/` → server, **lease, mtls**» | Misma falsedad que C3, y además **omite `transport/` entero** —el paquete de `MaxMessageBytes` y `ControlSessionID`— y `internal/enroll/` |
| C5 | `README.md:34` — «Go **1.26.0**» | `go.mod:3` dice **`1.26.5`**, igual que `Makefile:4` y `ci.yml:14` |
| C6 | `README.md:36` y `CLAUDE.md:27` — «mTLS **+ token de plataforma**» | **Ese token no existe** ni en el código ni en el contrato. Los únicos «token» del repo son los del **usuario operador** (ADR-0025), que son **carga relayada al IAM**, no autenticación del canal. La autenticación del canal es mTLS y nada más |
| C7 | `README.md:43-52` (sección entera) y `CLAUDE.md:154-155` — el umbral «`inline` vs URL prefirmada» | **`inline` no existe**: es `reserved 10` desde el 2026-08-12 y `SendMedia.src` tiene **una sola rama**. No hay umbral que decidir. El propio README lo dice bien 70 líneas más abajo: **segunda auto-contradicción** |
| C8 | `README.md:210` — «compatibilidad hacia atrás **garantizada** por `buf breaking`» | **Tercera auto-contradicción**: `:128-131` dice que la Ola 1.6 del Plan 044 rompe a propósito con cuatro hallazgos que «no se apaciguan». Y **ningún target ni CI corre `buf breaking`** (D-1): no hay nada garantizado por un gate |
| C9 | `README.md:88` y `CLAUDE.md:100` llaman al campo del Heartbeat **`session_state`** | Se llama **`state`** (`SessionState state = 4;`). `grep -rn session_state` sobre el `.proto` devuelve **cero** |
| C10 | `README.md:88` describe el `Heartbeat` con cinco elementos | Le falta **`inference_readiness` (campo 6)**, que es de `v0.17.0` — la versión que el propio repo publica. El README se quedó en v0.16.0 |
| C11 | `CLAUDE.md:12` — «contiene el servidor gRPC del lado cloud» | Contiene una **implementación de referencia/demo**. El de producción vive en `wapp-cloud-platform`. El propio CLAUDE.md se corrige 120 líneas después |
| C12 | `CLAUDE.md:106-109` — «**campos obligatorios** en todos los mensajes: `session_id`, `command_id`» | **No son obligatorios**: proto3 no tiene `required`, y el contrato declara los casos donde el vacío es correcto **y no es un error** |

**Nota justa**: `CLAUDE.md:49-59` ya había aprendido la lección —retiró el número de versión de su
encabezado y puso el comando para consultarlo, advirtiendo de que `git tag | tail` miente—. El
`README.md` es el que no recibió ese tratamiento.

---

## 3. Lo que NO es deuda (comprobado, para que nadie lo «arregle»)

- ✅ **Cero credenciales en git.** `git ls-files certs/` está vacío; `.gitignore:19-23` excluye
  `certs/`, `*.key` y `*.pem`. Los ficheros existen en disco desde el 2026-06-26, ignorados.
- ✅ **La concurrencia es correcta donde importa.** `stream.Send` serializado por mutex en los dos
  lados (`internal/server/cloudlink.go:19-28`, `client/client.go:70-76`); `deregister` solo borra si
  el mapeo sigue apuntando a **ese** `conn`, para no dejar sin ruta a una reconexión más nueva;
  `MemoryStore.Consume` valida y marca usado bajo el **mismo** lock. Los tests corren con `-race`.
- ✅ **El lease firma y verifica sobre los MISMOS bytes embebidos** (`lease/lease.go:54-72`),
  evitando toda la clase de bugs de encoding canónico. No lo «modernices» a un sub-mensaje proto:
  proto3 no garantiza serialización byte-estable entre versiones, y el porqué está escrito ahí.
- ✅ **`internal/server` bajo `internal/` es deliberado**, no un olvido: la barrera la impone el
  compilador. Verificado que ningún consumidor lo importa.
- ✅ **`gen/` commiteado es deliberado**: sin él no habría contrato consumible cross-repo.
- ✅ **Las tres reglas de `buf lint` exceptuadas** (`SERVICE_SUFFIX`,
  `RPC_REQUEST_STANDARD_NAME`, `RPC_RESPONSE_STANDARD_NAME`) están **documentadas con su motivo**
  en `buf.yaml`. Son nombres de dominio elegidos, no un descuido.
- ✅ **`ci.yml` en `workflow_dispatch`** es una decisión del ecosistema (régimen local desde el
  2026-08-01), no un workflow roto. Lo que sí es deuda es que el gate local **no incluya `buf`**.
