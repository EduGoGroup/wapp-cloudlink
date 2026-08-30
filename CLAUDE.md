# CLAUDE.md — `wapp-cloudlink`

> **Portal. La verdad vive en [`documentations/`](documentations/README.md); esto solo apunta.** El
> `README.md` de este repo arrastra **doce afirmaciones falsas verificadas** (inventario en
> `documentations/deuda.md`, §2): no lo uses como fuente.

## Qué es esta pieza

El **contrato** entre el núcleo que corre 24/7 en el equipo del cliente (*Edge Agent*) y la
plataforma cloud: un `.proto` de 783 líneas, su generado, y cinco paquetes públicos que los dos
extremos importan para no divergir. 🔴 **Es un contrato, no un servicio**: nadie lo despliega, se
consume por tag. El servidor de producción vive en `wapp-cloud-platform`; `internal/server/` es
**referencia** y los dos `cmd/` son arneses.

`main` en `b52e985` = **`v0.17.0`** (HEAD == tag). Go **1.26.5**. **Dos** dependencias directas:
gRPC y protobuf. Sin base de datos, sin HTTP, sin frontend.

## Las cinco reglas innegociables

1. **No reutilices un número `reserved`.** Son cinco pares (número **y** nombre):
   `run_flow_step`(12), `delivery`(11), `SendMedia.inline`(10), `IncomingMessage.intent`(11),
   `SensitivePayload.intent`(5). Reutilizar uno hace que un Edge viejo interprete otra cosa en ese
   hueco **sin un solo error**. Los cambios son **aditivos por defecto**.

2. **`gen/`, `client/`, `lease/`, `mtls/` y `transport/` son PÚBLICOS y están en la raíz.** Los
   consumidores los importan 101, 4, 13, 7 y 5 veces: moverlos bajo `internal/` —cosa que el
   `README.md` viejo afirma que ya están— **deja el ecosistema sin compilar**.

3. **Zero-knowledge y doble llave.** La **DEK** (descifra el store de whatsmeow) la custodia el
   **cliente** y **jamás es un campo de este contrato**; el **Lease** lo emite y **revoca** el
   servidor — kill-switch anti-clon, gate 2-de-2 (`CanOperate = hasDEK ∧ leaseVigente`), validado
   sobre el **payload firmado** y nunca sobre los campos top-level. Zero-knowledge protege
   **llaves**, no el contenido de negocio: ese **sí** sube a la nube, a propósito.

4. **Nada de dependencias.** Prohibido importar un repo `edugo-*`: el ecosistema usa
   **copia-adaptación**, nunca dependencia (que el módulo se llame `EduGoGroup/…` es la org de
   GitHub, no una dependencia). **`wapp-shared` tampoco entra aquí** — es el monorepo interno de
   wApp, con releases por módulo (tags `<modulo>/vX.Y.Z`), y este contrato se mantiene delgado a
   propósito. Sin broker ni Redis en el Edge: concurrencia con Go, durabilidad por su outbox SQLite.

5. **Aquí no hay gate que te salve.** `ci.yml` es `workflow_dispatch`: **un PR no valida nada** y
   el gate real es `make ci-local` en tu máquina — que **no corre `buf lint`**, y **`buf breaking`
   no lo corre nadie**. Antes de tocar el `.proto`, a mano: `make lint` ·
   `buf breaking --against '.git#branch=main'` · `make generate && git diff --exit-code gen/`. Y al
   leer tests **cuenta los SKIP**: un `rc=0` los cuenta igual que un PASS.

## Índice de `documentations/`
| Documento | Para qué |
|---|---|
| [`documentations/README.md`](documentations/README.md) | Portal de la pieza y estado actual |
| [`documentations/constitucion.md`](documentations/constitucion.md) | **Empieza aquí.** Los 14 invariantes propios + los del ecosistema, cada uno con cómo se comprueba y qué test lo vigila; tecnología real; **diez trampas conocidas** |
| [`documentations/arquitectura.md`](documentations/arquitectura.md) | Capas, los diez paquetes, los dos binarios y dos diagramas |
| [`documentations/contratos.md`](documentations/contratos.md) | Los 2 rpc, **los 8 frames nube→Edge y los 10 Edge→nube**, 6 enums, **los 5 retirados con fecha y motivo**, constantes compartidas, variables de entorno |
| [`documentations/operacion.md`](documentations/operacion.md) | Arranque local, qué valida cada `make`, cómo se corta una versión **a mano** (no hay `release.yml`), depuración |
| [`documentations/deuda.md`](documentations/deuda.md) | Las 14 deudas con `fichero:línea` y las 12 afirmaciones falsas del `README.md` |

**Dos atajos que evitan errores caros.** El último tag se consulta con
`git for-each-ref --sort=-creatordate --format='%(refname:short)' refs/tags | head -1` — ⚠️
`git tag | tail` ordena lexicográficamente y **miente** (`v0.15.0 < v0.9.0`). Y el `.proto` es lo
único que se edita a mano: `gen/` lo escribe `buf generate` y se commitea.
