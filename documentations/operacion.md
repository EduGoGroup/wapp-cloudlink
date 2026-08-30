# Operación de `wapp-cloudlink`

> ⚠️ **Esta pieza no se despliega.** Es un módulo Go que se consume por tag. «Operarla» significa
> generarla, probarla y publicarla — no arrancarla en un servidor. Los dos binarios de `cmd/` son
> arneses de desarrollo local y ninguno corre en UAT ni en producción.
>
> Verificado en la máquina de UAT: **no hay checkout de `wapp-cloudlink`**. Entra como módulo Go
> dentro de los binarios de la plataforma y del Edge, los dos compilados contra `v0.17.0`.

---

## 1. 🔴 El aviso que gobierna todo lo demás

**Un PR no valida nada en este repo.** `.github/workflows/ci.yml` es `on: workflow_dispatch` y
nada más (`ci.yml:10-11`): no se dispara con `push` ni con `pull_request`. El régimen desde el
2026-08-01 es **local**, y el propio fichero lo explica.

El único workflow que se dispara solo es `sync-main-to-dev.yml` (`push` a `main`), y **no valida
nada**: hace fast-forward de `dev` sobre `main` para que no queden desincronizadas. Es la excepción
declarada, y existe porque olvidarlo ya costó una reparación a mano.

**⇒ El gate real es `make ci-local` en tu máquina, y nadie lo comprueba por ti.**

**Y un `rc=0` no significa que se haya probado algo.** `go test` devuelve 0 contando igual un
`--- SKIP` que un `--- PASS`. Cuenta los SKIP siempre:

```bash
GOWORK=off go test -race ./... -count=1 -v 2>&1 | tee /tmp/cl.log
grep -c -- '--- PASS' /tmp/cl.log
grep -c -- '--- SKIP' /tmp/cl.log      # tiene que ser 0 en este repo
grep -c -- '--- FAIL' /tmp/cl.log
```

En **este** repo concreto los tests de integración de otras piezas no aplican (no hay base de datos
ni Docker), así que un SKIP aquí es una señal rara: investígalo. Lo que sí es una trampa real es que
los dos `Fuzz*` **solo corren su corpus semilla** en un `go test` normal; sin `-fuzz` no fuzzean
nada, y ningún target del `Makefile` pasa ese flag.

---

## 2. Arranque en local

### 2.1 Requisitos

- **Go 1.26.5** exacto (`go.mod:3`, `Makefile:4`, `ci.yml:14`).
- `buf`, `protoc-gen-go` y `protoc-gen-go-grpc` **solo si vas a tocar el `.proto`**:
  `make tools`.
- Docker **solo** para `make ci-docker`.
- Base de datos: **ninguna**. Red externa: **ninguna** (los tests usan `bufconn` en memoria).

### 2.2 PKI de desarrollo

```bash
./scripts/gen-dev-certs.sh                 # CN de Edge por defecto: edge-dev-001
EDGE_CN=edge-acme-7 ./scripts/gen-dev-certs.sh
```

Genera en `certs/` una CA autofirmada, el cert de servidor (SAN `localhost` + `127.0.0.1`,
`serverAuth`) y el cert de Edge (`clientAuth`), todos EC P-256, 825 días. Es idempotente.
🔒 `certs/`, `*.key` y `*.pem` están en `.gitignore`: **nunca los commitees**.

### 2.3 Los dos binarios

```bash
cp .env.example .env         # define CLOUDLINK_ADDR=:8101 y CLOUDLINK_CERT_DIR=certs
set -a; . ./.env; set +a

GOWORK=off go run ./cmd/cloudlink     # servidor standalone
GOWORK=off go run ./cmd/democloud     # arnés de "nube de demostración"
```

🔴 **`cmd/cloudlink` arranca sin mTLS si no encuentra los tres ficheros de cert** — un
`CLOUDLINK_CERT_DIR` mal escrito levanta un CloudLink abierto y solo lo dice un `log.Printf`. Si
esperas mTLS, **verifica la línea del log**: `cloudlink: mTLS activo (certs en "certs")` frente a
`cloudlink: SIN mTLS (no se hallaron certs en …)`.

`cmd/democloud` es **siempre insecure y sin lease, a propósito**, y acepta órdenes por stdin
(`send`, `ping`, `quit`) o por el fichero de `CLOUDLINK_CMD_FILE`. Ver [`contratos.md`](contratos.md).

---

## 3. Los `make` y qué valida cada uno

Los nueve targets reales del `Makefile`. **Todos los de Go fuerzan `GOWORK=off`**, para ignorar
cualquier `go.work` local y compilar contra el `go.mod` de verdad.

| Target | Qué corre | Qué valida de verdad |
|---|---|---|
| `make fmt-check` | `gofmt -l .` | Falla si hay **cualquier** fichero sin formatear. Es el primero por barato |
| `make vet` | `go vet ./...` | Análisis estático de la stdlib |
| `make lint-go` | `golangci-lint run --timeout=5m` | ⚠️ **Sin fichero de configuración**: `find . -name '.golangci*'` no devuelve nada, así que corre con los linters por defecto de la versión instalada. Qué se comprueba depende de tu máquina, no del repo |
| `make test` | `go test -race ./... -count=1` | Los **41** tests. `-race` de verdad; `-count=1` evita caché |
| `make build` | `go build ./...` | Compila todo, incluidos los dos `cmd/` |
| **`make ci-local`** | `fmt-check vet lint-go test build` | 🔴 **El gate de pre-push.** Cinco targets — y **`buf lint` no es uno de ellos** (`Makefile:42`) |
| `make ci-docker` | lo mismo dentro de `golang:1.26.5-bookworm` + golangci-lint `v2.12.2` | Replica el toolchain fijado. Úsalo cuando `ci-local` esté verde y quieras descartar que sea tu máquina |
| `make generate` | `buf generate` | Regenera `gen/`. **Nunca `protoc` directo** |
| `make lint` | **`buf lint`** — el linter del **protobuf**, no de Go | Convenciones del `.proto`. `buf.yaml` exceptúa `SERVICE_SUFFIX`, `RPC_REQUEST_STANDARD_NAME` y `RPC_RESPONSE_STANDARD_NAME`, documentadas |
| `make tools` | instala `buf`, `protoc-gen-go`, `protoc-gen-go-grpc` | ⚠️ instala **`buf@latest`**, sin pinar, mientras Go y golangci-lint sí están pinados |

### 3.1 🔴 El gate que falta, y por qué es el riesgo número uno

Para un repo que **es** un contrato, lo que hay que vigilar es la compatibilidad. Y:

- **`buf lint` está FUERA de `make ci-local`.** Los cinco targets del agregado no lo incluyen.
- **`buf breaking` no lo corre NADIE**: ni un target del `Makefile`, ni ningún workflow — aunque
  `buf.yaml` lo tenga configurado con la regla `FILE`.

⇒ **Romper el contrato aquí pasa los cinco gates en verde.** Antes de tocar el `.proto`, córrelos a
mano:

```bash
make lint                                   # buf lint
buf breaking --against '.git#branch=main'   # compatibilidad contra main
make generate && git diff --exit-code gen/  # el generado está al día
```

Si `buf breaking` reporta algo, **es una decisión, no un despiste**: escríbela en el `CHANGELOG.md`
con su motivo. Ya ha pasado una vez a propósito (la retirada del `intent` el 2026-08-24, con cuatro
hallazgos que **no se apaciguaron**).

### 3.2 Qué NO piden los tests

Ni Docker, ni base de datos, ni red real: el transporte se prueba con
`google.golang.org/grpc/test/bufconn` (en memoria) y los certs de los tests de mTLS se **generan
efímeros en memoria**, no se leen de `certs/`. Un `make test` en limpio, sin PKI y sin nada
levantado, tiene que salir verde. Si no sale, el problema es real.

### 3.3 Los 41 tests, por dónde miran

| Fichero | N | Qué vigila |
|---|---|---|
| `gen/wapp/cloudlink/v1/cloudlink_contract_test.go` | 16 | Roundtrips y **compatibilidad de wire** de cada campo nuevo, simulando emisores y receptores viejos con `dynamicpb`/`protodesc`. Es el candado más valioso del repo |
| `internal/server/transport_test.go` | 6 | Correlación bidi, sesión desconocida, baja y reconexión, concurrencia, propagación del error al cliente |
| `lease/lease_test.go` | 6 | El gate 2-de-2, el kill-switch, la expiración, la firma inválida, el anti-replay y la revocación e2e por el stream |
| `internal/enroll/enroll_test.go` | 5 | Enrolar y después conectar por mTLS; código inválido, expirado y ya usado; `cloud_enc_pubkey` |
| `internal/server/lease_renewal_test.go` | 2 | El Heartbeat renueva; sin `Issuer` inyectado no renueva |
| `mtls/mtls_test.go` · `internal/enroll/store_test.go` · `internal/server/reception_test.go` · `internal/server/limits_test.go` | 1 c/u | Handshake mTLS; GC de códigos; backpressure por sesión; el límite de 4 MiB |
| `lease/fuzz_test.go` · `internal/enroll/fuzz_test.go` | 1 c/u | `FuzzOpen` (el sobre del lease) y `FuzzParseAndVerifyCSR` |

🔴 **Lo que NO tiene candado**: los cuatro `reserved` distintos del `intent`; la ausencia de PII en
los frames de telemetría; `lease_pubkey` (campo 5 de `EnrollEdgeResponse`); y que el gate del lease
esté **encendido** en un despliegue real.

Para fuzzear de verdad, a mano:

```bash
GOWORK=off go test ./lease -run=Fuzz -fuzz=FuzzOpen -fuzztime=60s
```

---

## 4. Publicar una versión

🔴 **Este repo NO tiene `release.yml`.** El tag y el GitHub Release van **a mano**. Y el flujo de
ramas del ecosistema manda: el trabajo aterriza en `dev` y **a `main` se pasa al final**; el tag se
corta sobre `main`.

### 4.1 Antes de nada, cuál es el último tag

```bash
git for-each-ref --sort=-creatordate --format='%(refname:short)' refs/tags | head -1
```

⚠️ **No uses `git tag | tail`**: ordena lexicográficamente y miente (`v0.15.0 < v0.9.0`). Con los 18
tags de hoy te devolvería `v0.9.0`.

### 4.2 El procedimiento

1. **El `.proto` primero.** `make generate`, y confirma que `git diff gen/` trae exactamente lo que
   esperas.
2. **Los dos gates de contrato a mano**: `make lint` y `buf breaking --against '.git#branch=main'`.
   Si rompe, decídelo y escríbelo.
3. **`make ci-local` verde**, y si dudas de tu máquina, `make ci-docker`.
4. **`CHANGELOG.md`**: nueva sección `## [X.Y.Z] - AAAA-MM-DD` con el porqué, no solo el qué. El
   `## [Unreleased]` queda vacío. Este CHANGELOG está **limpio y verificado**: cada entrada que se
   contrastó (`lease_pubkey` en 0.12.0, `max_output_tokens`/`class`/`warmup` en 0.16.0,
   `inference_readiness` en 0.17.0) existe en el `.proto` y en el `.pb.go`. **Mantenlo así.**
5. **`dev` → `main`**, tag anotado `vX.Y.Z` sobre `main`, y push del tag.
6. **Realinea los consumidores**: `go get github.com/EduGoGroup/wapp-cloudlink@vX.Y.Z` en
   `wapp-cloud-platform` y en `wapp-edge-agent`, `go mod tidy`, y el `ci-local` de **cada uno**.
   Un cambio de proto deja a los dos consumidores en rojo hasta que se realinean.

### 4.3 La invariante de estado sano

```bash
git rev-parse HEAD          # debe coincidir con
git rev-list -n1 $(git for-each-ref --sort=-creatordate --format='%(refname:short)' refs/tags | head -1)
```

Hoy coinciden (`b52e985` = `v0.17.0`): **nada publicado sin taguear, nada tagueado sin publicar**.
Si divergen, alguien dejó trabajo en `main` sin cortar versión, y los consumidores no lo tienen.

---

## 5. Depurar cuando falla

### «El Edge y la nube no se entienden» tras un cambio de contrato

Casi siempre es **desalineamiento de versión**, no un bug. Comprueba los tres a la vez:

```bash
git -C <repo>/cloud/wapp-cloudlink   describe --tags
grep wapp-cloudlink <repo>/cloud/wapp-cloud-platform/go.mod
grep wapp-cloudlink <repo>/edge/wapp-edge-agent/go.mod
```

Y en la máquina donde corre, el binario **vivo** no es el fichero instalado: pregunta por el
buildinfo del proceso.

```bash
go version -m /proc/$(systemctl show -p MainPID --value <unidad>)/exe | grep cloudlink
```

### «Un campo nuevo llega vacío»

Tres causas, en orden de frecuencia:

1. El emisor no lo puebla (mira quién construye el mensaje, no el `.proto`).
2. El consumidor está en un tag anterior: el campo se ignora en silencio, que es justo lo que
   proto3 promete.
3. Es un `optional` y estás leyendo el getter en vez de la **presencia**. Con `optional`, «0» y «no
   dije nada» son cosas distintas; usa `Has…()`.

### «`GOAWAY too_many_pings`»

Divergencia de keepalive. Los parámetros (PING 30 s, Timeout 10 s, `MinTime` 15 s,
`PermitWithoutStream`) están copiados **en tres sitios** que nada ata: `cmd/cloudlink/main.go:26-37`,
`cmd/democloud/main.go:51-60` y el servidor real de `wapp-cloud-platform`. Compara los tres.

### «El servidor de demo deja de entregar frames»

Mira el hook de saturación: `cmd/cloudlink` loguea
`cloudlink: SATURACIÓN sesión="…" descartados=N`. Si N crece, el consumidor va lento y el inbox de
64 está descartando. Es **por diseño**: la durabilidad la da el outbox del Edge.

⚠️ Y si los frames que se pierden **no llevan `session_id`**, la causa puede ser otra: todos los
frames sin sesión de **todos** los Edges caen en un mismo inbox compartido bajo la clave vacía. Ver
[`deuda.md`](deuda.md).

### «El kill-switch no corta»

Recuerda que el `Validator` **no persiste**: al reiniciar el Edge vuelve a «sin lease aplicado». Y
que la revocación es **pegajosa** dentro de la vida del proceso: un lease válido posterior no la
levanta. Si un envío sale con lease revocado, el problema no está en `lease/` —está cubierto por
seis tests— sino en si el gate está **cableado y encendido** en el Edge.

### Rastro de ejecución que sí existe

Este repo **no tiene logger estructurado**: los dos `cmd/` usan el `log` de la stdlib. Lo que se
registra en ejecución, y con qué regla:

| Se registra | Cuándo | Dónde |
|---|---|---|
| `cloudlink: mTLS activo (certs en …)` **o** `cloudlink: SIN mTLS …` | Siempre al arrancar, según encuentre o no los **tres** ficheros | `cmd/cloudlink` |
| `cloudlink: escuchando en <addr>` | Al abrir el listener | `cmd/cloudlink` |
| `SATURACIÓN sesión=… descartados=N` | **Cada** descarte por inbox lleno, vía el `WithSaturationHook` | los dos `cmd/` |
| Cada `EdgeToCloud` recibido, con su tipo | Siempre, mientras el arnés corre | `cmd/democloud` |
| `democloud: leyendo comandos de … (tail-poll)` | Solo si `CLOUDLINK_CMD_FILE` está puesta | `cmd/democloud` |

Los paquetes de librería (`lease/`, `mtls/`, `transport/`, `client/`) **no loguean nada**: devuelven
errores. Es deliberado — quien los importa decide cómo los registra.
