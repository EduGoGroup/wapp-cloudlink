# `wapp-cloudlink` — documentación de la pieza

**Qué es.** El **contrato** entre el núcleo que corre en el equipo del cliente (el *Edge Agent*) y
la plataforma cloud de wApp: un fichero `.proto` de 783 líneas, su código Go generado, y cinco
paquetes públicos que los dos extremos importan para no divergir. **No es un servicio**: nadie lo
despliega. Es un módulo Go que se publica por tags y que hoy consumen exactamente dos repos.

**Para qué existe.** Los dos extremos viven en repos distintos y no comparten nada más que esto.
Si el frame, el límite de tamaño, el formato del lease o el id de la sesión de control se
definieran dos veces, su divergencia **no daría error de compilación**: daría un fallo de campo.
Por eso todo lo que los dos lados tienen que saber igual vive aquí y solo aquí.

**Lo que NO es.** El servidor gRPC de producción **no está aquí** — vive en `wapp-cloud-platform`.
Lo que este repo tiene bajo `internal/server/` es una implementación de referencia y demo, bajo
`internal/` a propósito para que nadie la importe.

---

## Estado hoy (2026-08-30)

| Dato | Valor |
|---|---|
| Rama · HEAD | `main` · `b52e985` |
| Último tag | **`v0.17.0`** (2026-08-24) — **HEAD == `v0.17.0`**, nada publicado sin taguear |
| Consumidores | `wapp-cloud-platform` y `wapp-edge-agent`, **ambos en `v0.17.0`** |
| Go | `1.26.5` |
| Dependencias directas | **dos**: `google.golang.org/grpc` y `google.golang.org/protobuf` |
| Persistencia | **ninguna**: cero SQL, cero migraciones, cero esquema |
| Superficie | 2 servicios · 2 rpc · 28 mensajes · 6 enums · 5 pares `reserved` |

---

## Índice

| Documento | Qué encontrarás |
|---|---|
| [`constitucion.md`](constitucion.md) | **Empieza aquí.** Los invariantes que no se pueden violar (los del ecosistema que aplican y los propios), cómo se comprueba cada uno, la tecnología real y **las trampas** en las que cae quien no las conoce. |
| [`arquitectura.md`](arquitectura.md) | Las capas, el mapa de los diez paquetes con una frase cada uno, los dos binarios y dos diagramas: la topología y el ciclo de vida del stream. |
| [`contratos.md`](contratos.md) | El corazón: los 2 rpc, **los 8 frames nube→Edge y los 10 Edge→nube**, los 6 enums, **los 5 campos retirados con su fecha y su motivo**, las constantes compartidas, las variables de entorno y los ficheros que se tocan. |
| [`operacion.md`](operacion.md) | Cómo se arranca en local, qué valida cada `make`, cómo se corta una versión **a mano** (este repo no tiene `release.yml`) y cómo se depura. |
| [`deuda.md`](deuda.md) | La deuda viva con `fichero:línea`: la fuga de mapa y goroutines del servidor de referencia, el inbox de clave vacía, el campo del contrato sin implementación, y **las doce afirmaciones falsas del `README.md` y el `CLAUDE.md` viejos**. |

---

## Los cinco invariantes que más se violan aquí

Están desarrollados en [`constitucion.md`](constitucion.md); van aquí porque son los que más caro
salen:

1. **No reutilices un número `reserved`.** Hay cinco. Un Edge viejo interpretaría otra cosa en ese
   hueco y no habría ningún error: rompería en silencio.
2. **`lease/`, `mtls/`, `transport/`, `client/` y `gen/` son PÚBLICOS.** Moverlos bajo `internal/`
   —cosa que la documentación vieja afirmaba que ya estaban— dejaría el ecosistema sin compilar.
3. **La DEK jamás es un campo de este contrato.** Ni cifrada, ni envuelta, ni «solo un hash».
4. **`buf lint` no está en `make ci-local` y `buf breaking` no lo corre nadie.** Para un repo que
   *es* un contrato, ese es el riesgo número uno: córrelos a mano.
5. **Un `PR` no valida nada**: `ci.yml` es `workflow_dispatch`. El gate real es local.
