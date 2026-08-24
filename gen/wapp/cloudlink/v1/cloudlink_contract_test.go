package cloudlinkv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Contrato de wapp.cloudlink.v1: roundtrips de los frames y compatibilidad de
// wire de cada cambio. Cubre ConfigUpdate (ADR-0021), la telemetría de salud y
// el diagnóstico remoto (Plan 031/ADR-0023), y el par de inferencia del Plan
// 044 · Ola 1.6 (ADR-0045).
//
// 2026-08-24: los dos tests del intent adjunto (el sellado dentro de
// SensitivePayload y el espejo en claro de IncomingMessage) murieron con el
// campo que probaban — la clasificación pasó de push a pull (ADR-0045 §4). Lo
// que ocupa su lugar es TestIncomingMessage_RetiredIntentFromOldEdge: durante el
// despliegue habrá Edges viejos que sigan adjuntando el campo 11, y lo que hoy
// hay que probar es que eso NO rompe al Cloud nuevo.

// El sobre sellado del entrante sigue vivo: roundtrip de SensitivePayload sin el
// intent que se retiró.
func TestSensitivePayload_Roundtrip(t *testing.T) {
	in := &SensitivePayload{
		Text:     "quiero 2 panes integrales",
		PushName: "Cliente",
		FromPn:   "593999999999",
		FromLid:  "111111111111111",
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal SensitivePayload: %v", err)
	}
	var out SensitivePayload
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal SensitivePayload: %v", err)
	}
	if out.GetText() != in.GetText() || out.GetPushName() != in.GetPushName() {
		t.Errorf("text/push_name = %q/%q", out.GetText(), out.GetPushName())
	}
	if out.GetFromPn() != in.GetFromPn() || out.GetFromLid() != in.GetFromLid() {
		t.Errorf("from_pn/from_lid = %q/%q", out.GetFromPn(), out.GetFromLid())
	}
}

func TestConfigUpdate_Roundtrip(t *testing.T) {
	in := &CloudToEdge{
		CommandId: "cmd-1",
		SessionId: "sess-1",
		Payload: &CloudToEdge_ConfigUpdate{
			ConfigUpdate: &ConfigUpdate{
				CommandId: "cmd-1",
				SessionId: "sess-1",
				Kind:      "intents",
				Version:   "intents-20260710",
				Payload:   []byte(`{"intents":[]}`),
			},
		},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal CloudToEdge: %v", err)
	}
	var out CloudToEdge
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal CloudToEdge: %v", err)
	}
	cu := out.GetConfigUpdate()
	if cu == nil {
		t.Fatalf("config_update nil tras el roundtrip")
	}
	if cu.GetKind() != "intents" || cu.GetVersion() != "intents-20260710" {
		t.Errorf("kind/version = %q/%q, want intents/intents-20260710", cu.GetKind(), cu.GetVersion())
	}
	if string(cu.GetPayload()) != `{"intents":[]}` {
		t.Errorf("payload = %q, want %q", cu.GetPayload(), `{"intents":[]}`)
	}
}

// Compatibilidad hacia atrás: un receptor que NO conoce el frame nuevo (campo 15)
// debe parsear un CloudToEdge{ConfigUpdate} SIN error, leyendo los campos base y
// tratando el frame desconocido como unknown field. Se simula con un descriptor
// "legacy" de CloudToEdge que solo declara command_id(1)/session_id(2) — el shape
// previo al Plan 029 — y se parsea el wire real del frame nuevo sobre él.
func TestCloudToEdge_ConfigUpdate_ForwardCompatOldReceiver(t *testing.T) {
	newMsg := &CloudToEdge{
		CommandId: "cmd-9",
		SessionId: "sess-9",
		Payload: &CloudToEdge_ConfigUpdate{
			ConfigUpdate: &ConfigUpdate{Kind: "intents", Version: "v1"},
		},
	}
	wire, err := proto.Marshal(newMsg)
	if err != nil {
		t.Fatalf("marshal newMsg: %v", err)
	}

	legacyMD := legacyCloudToEdgeDescriptor(t)
	legacy := dynamicpb.NewMessage(legacyMD)
	if err := proto.Unmarshal(wire, legacy); err != nil {
		t.Fatalf("un receptor viejo no debe fallar al parsear ConfigUpdate: %v", err)
	}

	cmdID := legacy.Get(legacyMD.Fields().ByName("command_id")).String()
	sessID := legacy.Get(legacyMD.Fields().ByName("session_id")).String()
	if cmdID != "cmd-9" || sessID != "sess-9" {
		t.Errorf("campos base perdidos: command_id=%q session_id=%q", cmdID, sessID)
	}
	// El campo 15 (config_update) es desconocido para el receptor viejo: no rompe
	// y se retiene como unknown field (se puede reenviar intacto).
	if len(legacy.GetUnknown()) == 0 {
		t.Errorf("el frame nuevo debía retenerse como unknown field, no vacío")
	}
}

// --- Plan 031 / ADR-0023: telemetría de salud + diagnóstico remoto ---

// El SessionHealth viaja adjunto al Heartbeat: roundtrip completo de un snapshot.
func TestSessionHealth_RoundtripInHeartbeat(t *testing.T) {
	in := &Heartbeat{
		LeaseCounter: 7,
		SelfPn:       "593999999999",
		State:        SessionState_SESSION_STATE_UNSPECIFIED,
		SessionHealth: &SessionHealth{
			WhatsappSocketState:  WhatsappSocketState_WHATSAPP_SOCKET_STATE_DEGRADED,
			DegradedReason:       "dek_load_timeout",
			LastInboundEventAgeS: 42,
			DekLoadDurationMs:    10500,
			IntentCircuit:        "half_open",
			OutboxDepth:          3,
			BinaryVersion:        "v0.9.0",
			DaemonUptimeS:        86400,
		},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal Heartbeat: %v", err)
	}
	var out Heartbeat
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal Heartbeat: %v", err)
	}
	h := out.GetSessionHealth()
	if h == nil {
		t.Fatalf("session_health nil tras el roundtrip")
	}
	if h.GetWhatsappSocketState() != WhatsappSocketState_WHATSAPP_SOCKET_STATE_DEGRADED {
		t.Errorf("whatsapp_socket_state = %v, want DEGRADED", h.GetWhatsappSocketState())
	}
	if h.GetDegradedReason() != "dek_load_timeout" {
		t.Errorf("degraded_reason = %q, want dek_load_timeout", h.GetDegradedReason())
	}
	if h.GetLastInboundEventAgeS() != 42 || h.GetDekLoadDurationMs() != 10500 {
		t.Errorf("edades/duraciones = %d/%d, want 42/10500", h.GetLastInboundEventAgeS(), h.GetDekLoadDurationMs())
	}
	if h.GetIntentCircuit() != "half_open" || h.GetOutboxDepth() != 3 {
		t.Errorf("intent_circuit/outbox = %q/%d, want half_open/3", h.GetIntentCircuit(), h.GetOutboxDepth())
	}
	if h.GetBinaryVersion() != "v0.9.0" || h.GetDaemonUptimeS() != 86400 {
		t.Errorf("binary_version/uptime = %q/%d, want v0.9.0/86400", h.GetBinaryVersion(), h.GetDaemonUptimeS())
	}
	// El lease_counter y self_pn base siguen intactos.
	if out.GetLeaseCounter() != 7 || out.GetSelfPn() != "593999999999" {
		t.Errorf("campos base del Heartbeat perdidos: lease=%d self_pn=%q", out.GetLeaseCounter(), out.GetSelfPn())
	}
}

// Compat: un receptor que NO conoce session_health (campo 5) parsea un Heartbeat
// nuevo SIN error, leyendo los campos base y reteniendo el campo 5 como unknown.
func TestHeartbeat_SessionHealth_ForwardCompatOldReceiver(t *testing.T) {
	newMsg := &Heartbeat{
		LeaseCounter:  9,
		SelfPn:        "593888888888",
		SessionHealth: &SessionHealth{WhatsappSocketState: WhatsappSocketState_WHATSAPP_SOCKET_STATE_CONNECTED},
	}
	wire, err := proto.Marshal(newMsg)
	if err != nil {
		t.Fatalf("marshal newMsg: %v", err)
	}
	legacyMD := legacyHeartbeatDescriptor(t)
	legacy := dynamicpb.NewMessage(legacyMD)
	if err := proto.Unmarshal(wire, legacy); err != nil {
		t.Fatalf("un receptor viejo no debe fallar al parsear session_health: %v", err)
	}
	if got := legacy.Get(legacyMD.Fields().ByName("lease_counter")).Int(); got != 9 {
		t.Errorf("lease_counter base perdido: %d", got)
	}
	if got := legacy.Get(legacyMD.Fields().ByName("self_pn")).String(); got != "593888888888" {
		t.Errorf("self_pn base perdido: %q", got)
	}
	if len(legacy.GetUnknown()) == 0 {
		t.Errorf("session_health debía retenerse como unknown field, no vacío")
	}
}

// Compat inversa: un emisor viejo (Heartbeat sin campo 5) decodifica sin problema
// en el shape nuevo; session_health queda nil (ausencia = "sin datos de salud").
func TestHeartbeat_OldSenderDecodesInNewShape(t *testing.T) {
	oldMD := legacyHeartbeatDescriptor(t)
	oldMsg := dynamicpb.NewMessage(oldMD)
	oldMsg.Set(oldMD.Fields().ByName("lease_counter"), protoreflect.ValueOfInt64(11))
	oldMsg.Set(oldMD.Fields().ByName("self_pn"), protoreflect.ValueOfString("593777777777"))
	wire, err := proto.Marshal(oldMsg)
	if err != nil {
		t.Fatalf("marshal oldMsg: %v", err)
	}
	var out Heartbeat
	if err := proto.Unmarshal(wire, &out); err != nil {
		t.Fatalf("el shape nuevo debe parsear un Heartbeat viejo: %v", err)
	}
	if out.GetLeaseCounter() != 11 || out.GetSelfPn() != "593777777777" {
		t.Errorf("campos base = %d/%q, want 11/593777777777", out.GetLeaseCounter(), out.GetSelfPn())
	}
	if out.GetSessionHealth() != nil {
		t.Errorf("session_health debía ser nil para un emisor viejo, got %v", out.GetSessionHealth())
	}
}

func TestDiagnosticsRequest_Roundtrip(t *testing.T) {
	in := &CloudToEdge{
		CommandId: "diag-1",
		SessionId: "sess-1",
		Payload: &CloudToEdge_DiagnosticsRequest{
			DiagnosticsRequest: &DiagnosticsRequest{
				CommandId: "diag-1",
				SessionId: "sess-1",
				Scope:     "full",
			},
		},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal CloudToEdge: %v", err)
	}
	var out CloudToEdge
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal CloudToEdge: %v", err)
	}
	dr := out.GetDiagnosticsRequest()
	if dr == nil {
		t.Fatalf("diagnostics_request nil tras el roundtrip")
	}
	if dr.GetCommandId() != "diag-1" || dr.GetScope() != "full" {
		t.Errorf("command_id/scope = %q/%q, want diag-1/full", dr.GetCommandId(), dr.GetScope())
	}
}

func TestDiagnosticsBundle_Roundtrip(t *testing.T) {
	in := &EdgeToCloud{
		CommandId: "diag-1",
		SessionId: "sess-1",
		Payload: &EdgeToCloud_DiagnosticsBundle{
			DiagnosticsBundle: &DiagnosticsBundle{
				CommandId:      "diag-1",
				LogTail:        "linea1\nlinea2",
				GoroutineDump:  "goroutine 1 [running]:",
				SubsystemsJson: `{"intent":{"circuit":"closed"}}`,
			},
		},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal EdgeToCloud: %v", err)
	}
	var out EdgeToCloud
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal EdgeToCloud: %v", err)
	}
	db := out.GetDiagnosticsBundle()
	if db == nil {
		t.Fatalf("diagnostics_bundle nil tras el roundtrip")
	}
	if db.GetCommandId() != "diag-1" || db.GetLogTail() != "linea1\nlinea2" {
		t.Errorf("command_id/log_tail = %q/%q", db.GetCommandId(), db.GetLogTail())
	}
	if db.GetSubsystemsJson() != `{"intent":{"circuit":"closed"}}` {
		t.Errorf("subsystems_json = %q", db.GetSubsystemsJson())
	}
}

// Compat: un receptor viejo de CloudToEdge (solo command_id/session_id) parsea un
// DiagnosticsRequest (campo 16) sin error, reteniéndolo como unknown field.
func TestCloudToEdge_DiagnosticsRequest_ForwardCompatOldReceiver(t *testing.T) {
	newMsg := &CloudToEdge{
		CommandId: "diag-9",
		SessionId: "sess-9",
		Payload:   &CloudToEdge_DiagnosticsRequest{DiagnosticsRequest: &DiagnosticsRequest{Scope: "logs"}},
	}
	wire, err := proto.Marshal(newMsg)
	if err != nil {
		t.Fatalf("marshal newMsg: %v", err)
	}
	legacyMD := legacyCloudToEdgeDescriptor(t)
	legacy := dynamicpb.NewMessage(legacyMD)
	if err := proto.Unmarshal(wire, legacy); err != nil {
		t.Fatalf("un receptor viejo no debe fallar al parsear diagnostics_request: %v", err)
	}
	if legacy.Get(legacyMD.Fields().ByName("command_id")).String() != "diag-9" {
		t.Errorf("command_id base perdido")
	}
	if len(legacy.GetUnknown()) == 0 {
		t.Errorf("diagnostics_request debía retenerse como unknown field, no vacío")
	}
}

// --- Plan 044 · Ola 1.6 / ADR-0045: el par de frames de inferencia ---

// El Cloud baja el prompt ya construido: roundtrip completo del request dentro de
// su CloudToEdge.
func TestInferenceRequest_RoundtripInCloudToEdge(t *testing.T) {
	in := &CloudToEdge{
		CommandId: "inf-1",
		Payload: &CloudToEdge_InferenceRequest{
			InferenceRequest: &InferenceRequest{
				CommandId:   "inf-1",
				Prompt:      "Clasifica: \"quiero 2 panes integrales\"",
				Format:      `{"type":"object","properties":{"intent":{"type":"string"}}}`,
				Temperature: new(float32(0)),
				TimeoutMs:   15000,
			},
		},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal CloudToEdge: %v", err)
	}
	var out CloudToEdge
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal CloudToEdge: %v", err)
	}
	req := out.GetInferenceRequest()
	if req == nil {
		t.Fatalf("inference_request nil tras el roundtrip")
	}
	if req.GetCommandId() != "inf-1" || req.GetTimeoutMs() != 15000 {
		t.Errorf("command_id/timeout_ms = %q/%d, want inf-1/15000", req.GetCommandId(), req.GetTimeoutMs())
	}
	if req.GetPrompt() != in.GetInferenceRequest().GetPrompt() {
		t.Errorf("el prompt no viajó verbatim: %q", req.GetPrompt())
	}
	if req.GetFormat() != in.GetInferenceRequest().GetFormat() {
		t.Errorf("format = %q", req.GetFormat())
	}
	// El campo previsto del prompt sellado nace vacío y así debe llegar.
	if len(req.GetEncPrompt()) != 0 {
		t.Errorf("enc_prompt debía viajar vacío, got %d bytes", len(req.GetEncPrompt()))
	}
}

// La razón de que temperature sea `optional`: 0.0 pedido explícitamente y "no
// dije nada" NO pueden ser el mismo byte en el cable. Si alguien le quita el
// optional, este test se pone rojo.
func TestInferenceRequest_TemperaturePresenceDistinguishesZeroFromUnset(t *testing.T) {
	cero, err := proto.Marshal(&InferenceRequest{CommandId: "t", Temperature: new(float32(0))})
	if err != nil {
		t.Fatalf("marshal con temperature=0: %v", err)
	}
	ausente, err := proto.Marshal(&InferenceRequest{CommandId: "t"})
	if err != nil {
		t.Fatalf("marshal sin temperature: %v", err)
	}
	if len(cero) == len(ausente) {
		t.Fatalf("temperature=0 y temperature ausente producen el MISMO wire (%d bytes): se perdió la presencia explícita", len(cero))
	}

	var conCero InferenceRequest
	if err := proto.Unmarshal(cero, &conCero); err != nil {
		t.Fatalf("unmarshal con temperature=0: %v", err)
	}
	if conCero.Temperature == nil {
		t.Errorf("temperature=0 llegó como ausente")
	} else if *conCero.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", *conCero.Temperature)
	}

	var sin InferenceRequest
	if err := proto.Unmarshal(ausente, &sin); err != nil {
		t.Fatalf("unmarshal sin temperature: %v", err)
	}
	if sin.Temperature != nil {
		t.Errorf("temperature ausente llegó poblada (%v)", *sin.Temperature)
	}
}

// La rama buena: la salida del modelo sube marshalada dentro del campo sellado.
// Aquí se prueba el TRANSPORTE del sub-mensaje (el sellado en sí es de envelope,
// que vive en wapp-shared y no es dependencia de este repo).
func TestInferenceResult_OutputBranchCarriesSubmessage(t *testing.T) {
	sellable, err := proto.Marshal(&InferenceOutput{RawJson: `{"intent":"pedido","confidence":0.87}`})
	if err != nil {
		t.Fatalf("marshal InferenceOutput: %v", err)
	}
	in := &EdgeToCloud{
		CommandId: "inf-1",
		Payload: &EdgeToCloud_InferenceResult{
			InferenceResult: &InferenceResult{
				CommandId: "inf-1",
				Result:    &InferenceResult_EncOutput{EncOutput: sellable},
			},
		},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal EdgeToCloud: %v", err)
	}
	var out EdgeToCloud
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal EdgeToCloud: %v", err)
	}
	res := out.GetInferenceResult()
	if res == nil {
		t.Fatalf("inference_result nil tras el roundtrip")
	}
	if res.GetCommandId() != "inf-1" {
		t.Errorf("command_id = %q, want inf-1 (correlación con el request)", res.GetCommandId())
	}
	if res.GetError() != InferenceError_INFERENCE_ERROR_UNSPECIFIED {
		t.Errorf("con salida no debe haber rama de error, got %v", res.GetError())
	}
	var payload InferenceOutput
	if err := proto.Unmarshal(res.GetEncOutput(), &payload); err != nil {
		t.Fatalf("unmarshal del sub-mensaje transportado: %v", err)
	}
	if payload.GetRawJson() != `{"intent":"pedido","confidence":0.87}` {
		t.Errorf("raw_json = %q", payload.GetRawJson())
	}
}

// La rama mala: el error nombrado viaja EN CLARO y excluye a la salida. El
// vocabulario es cerrado a propósito (enum, no string): el consumidor lo mapea a
// motivos de degradación.
func TestInferenceResult_ErrorBranchIsInClearAndExclusive(t *testing.T) {
	for _, e := range []InferenceError{
		InferenceError_INFERENCE_ERROR_OLLAMA_DOWN,
		InferenceError_INFERENCE_ERROR_BREAKER_OPEN,
		InferenceError_INFERENCE_ERROR_TIMEOUT,
		InferenceError_INFERENCE_ERROR_LEASE_INVALID,
		InferenceError_INFERENCE_ERROR_EDGE_SIN_CAPACIDAD,
	} {
		b, err := proto.Marshal(&InferenceResult{
			CommandId: "inf-2",
			Result:    &InferenceResult_Error{Error: e},
		})
		if err != nil {
			t.Fatalf("marshal con error %v: %v", e, err)
		}
		var out InferenceResult
		if err := proto.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal con error %v: %v", e, err)
		}
		if out.GetError() != e {
			t.Errorf("error = %v, want %v", out.GetError(), e)
		}
		if out.GetEncOutput() != nil {
			t.Errorf("%v: la rama de error no puede traer salida (got %d bytes)", e, len(out.GetEncOutput()))
		}
	}
}

// Compat del retiro (ADR-0045 §4): un Edge VIEJO que todavía adjunta el intent en
// el campo 11 no debe romper al Cloud nuevo. Durante el despliegue esto pasa de
// verdad —el proto se publica antes que el binario del Edge—, y el campo 11 quedó
// reservado justo para que ese hueco no se interprete como otra cosa.
func TestIncomingMessage_RetiredIntentFromOldEdge(t *testing.T) {
	oldMD := legacyIncomingMessageWithIntentDescriptor(t)
	oldMsg := dynamicpb.NewMessage(oldMD)
	oldMsg.Set(oldMD.Fields().ByName("from"), protoreflect.ValueOfString("593999999999@s.whatsapp.net"))
	oldMsg.Set(oldMD.Fields().ByName("text"), protoreflect.ValueOfString("quiero 2 panes integrales"))
	// El campo 11 llevaba un sub-mensaje; en el cable es un length-delimited, así
	// que unos bytes cualesquiera reproducen exactamente esa forma.
	oldMsg.Set(oldMD.Fields().ByName("intent"), protoreflect.ValueOfBytes([]byte{0x0a, 0x06, 'p', 'e', 'd', 'i', 'd', 'o'}))
	wire, err := proto.Marshal(oldMsg)
	if err != nil {
		t.Fatalf("marshal del entrante viejo: %v", err)
	}

	var out IncomingMessage
	if err := proto.Unmarshal(wire, &out); err != nil {
		t.Fatalf("el shape nuevo debe parsear un entrante con el campo 11 retirado: %v", err)
	}
	if out.GetFrom() != "593999999999@s.whatsapp.net" || out.GetText() != "quiero 2 panes integrales" {
		t.Errorf("campos base perdidos: from=%q text=%q", out.GetFrom(), out.GetText())
	}
	if len(out.ProtoReflect().GetUnknown()) == 0 {
		t.Errorf("el campo 11 retirado debía retenerse como unknown field, no descartarse")
	}
}

// legacyIncomingMessageWithIntentDescriptor construye el IncomingMessage previo
// al 2026-08-24: los campos base más el 11 (intent), que aquí se declara `bytes`
// porque en el cable un sub-mensaje y unos bytes son la misma cosa
// (length-delimited) y así el descriptor no necesita declarar el mensaje muerto.
func legacyIncomingMessageWithIntentDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	byt := descriptorpb.FieldDescriptorProto_TYPE_BYTES
	lbl := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("legacy_incoming.proto"),
		Syntax:  new("proto3"),
		Package: new("wapp.cloudlink.legacy"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("IncomingMessage"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: new("from"), Number: new(int32(1)), Type: &str, Label: &lbl, JsonName: new("from")},
				{Name: new("text"), Number: new(int32(2)), Type: &str, Label: &lbl, JsonName: new("text")},
				{Name: new("intent"), Number: new(int32(11)), Type: &byt, Label: &lbl, JsonName: new("intent")},
			},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("construir descriptor legacy IncomingMessage: %v", err)
	}
	return fd.Messages().Get(0)
}

// legacyHeartbeatDescriptor construye un Heartbeat previo al Plan 031: los campos
// base 1-4 (lease_counter/self_pn/self_jid/state) sin el campo 5 (session_health).
// Sirve para simular un receptor/emisor que no conoce la telemetría de salud.
func legacyHeartbeatDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	i64 := descriptorpb.FieldDescriptorProto_TYPE_INT64
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	i32 := descriptorpb.FieldDescriptorProto_TYPE_INT32
	lbl := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("legacy_heartbeat.proto"),
		Syntax:  new("proto3"),
		Package: new("wapp.cloudlink.legacy"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("Heartbeat"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: new("lease_counter"), Number: new(int32(1)), Type: &i64, Label: &lbl, JsonName: new("leaseCounter")},
				{Name: new("self_pn"), Number: new(int32(2)), Type: &str, Label: &lbl, JsonName: new("selfPn")},
				{Name: new("self_jid"), Number: new(int32(3)), Type: &str, Label: &lbl, JsonName: new("selfJid")},
				{Name: new("state"), Number: new(int32(4)), Type: &i32, Label: &lbl, JsonName: new("state")},
			},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("construir descriptor legacy Heartbeat: %v", err)
	}
	return fd.Messages().Get(0)
}

// legacyCloudToEdgeDescriptor construye el descriptor de un CloudToEdge previo al
// Plan 029: string command_id = 1; string session_id = 2; (sin el oneof ni el
// campo 15). Sirve para simular un receptor que no conoce config_update.
func legacyCloudToEdgeDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	lbl := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("legacy_cloudtoedge.proto"),
		Syntax:  new("proto3"),
		Package: new("wapp.cloudlink.legacy"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("CloudToEdge"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: new("command_id"), Number: new(int32(1)), Type: &str, Label: &lbl, JsonName: new("commandId")},
				{Name: new("session_id"), Number: new(int32(2)), Type: &str, Label: &lbl, JsonName: new("sessionId")},
			},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("construir descriptor legacy: %v", err)
	}
	return fd.Messages().Get(0)
}
