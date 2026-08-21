package transport

// ControlSessionID es el `session_id` que el Edge estampa en los frames de AUTENTICACIÓN
// (UserLogin, UserRefresh, UserLogout) cuando todavía no hay ninguna sesión de WhatsApp
// que pueda prestar el suyo.
//
// 🔴 NO ES UNA SESIÓN DE WHATSAPP, y ese es todo el punto de que esta constante exista.
// Es una RUTA: el gateway enruta la respuesta por `registry.Push(session_id)` y registra
// la sesión de forma perezosa al primer frame, así que el frame de auth DEBE llevar un
// session_id no vacío. Pero el operador puede loguearse en el PRIMER ARRANQUE, antes de
// emparejar ningún teléfono, cuando no existe ninguna sesión de WhatsApp de la que tomarlo.
// Un id de control fijo resuelve las dos cosas: es estable y existe siempre.
//
// ⚠️ QUÉ TIENE QUE HACER LA NUBE CON ÉL: registrarlo en el Registry (sin eso el login del
// operador no tiene por dónde volver) y NO PERSISTIRLO como sesión de flota. Persistirlo
// crea una fila en `fleet_sessions` que no corresponde a ningún teléfono — el cliente la ve
// en su dashboard como si lo fuera, con selector de perfil y como destino de envío. El
// porqué completo, y la validación de los ocho consumidores, en el micro-plan MP-11.
//
// 🔑 VIVE AQUÍ, en el módulo del contrato, PORQUE LOS DOS LADOS LO NECESITAN: el Edge para
// estamparlo y la nube para reconocerlo. Duplicarlo dejaría dos definiciones que nada ata,
// y su divergencia no daría error: reaparecería la fila fantasma, en silencio.
const ControlSessionID = "__wapp_control__"
