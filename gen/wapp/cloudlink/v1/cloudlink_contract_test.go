package cloudlinkv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Contrato aditivo del Plan 029/ADR-0020 y ADR-0021: verifica el roundtrip de
// ClassifiedIntent (por el camino sellado dentro de SensitivePayload y por el
// espejo en claro de IncomingMessage), el roundtrip de ConfigUpdate, y la
// compatibilidad hacia atrás de un CloudToEdge{ConfigUpdate} visto por un
// receptor que no conoce el frame nuevo.

func sampleIntent() *ClassifiedIntent {
	return &ClassifiedIntent{
		Intent:        "pedido",
		Params:        map[string]string{"producto": "pan integral", "cantidad": "2"},
		Confidence:    0.87,
		ConfigVersion: "intents-20260710",
	}
}

func assertIntentEqual(t *testing.T, got *ClassifiedIntent) {
	t.Helper()
	if got == nil {
		t.Fatalf("intent nil tras el roundtrip")
	}
	if got.GetIntent() != "pedido" {
		t.Errorf("intent = %q, want %q", got.GetIntent(), "pedido")
	}
	if got.GetConfidence() != 0.87 {
		t.Errorf("confidence = %v, want 0.87", got.GetConfidence())
	}
	if got.GetConfigVersion() != "intents-20260710" {
		t.Errorf("config_version = %q, want %q", got.GetConfigVersion(), "intents-20260710")
	}
	if got.GetParams()["producto"] != "pan integral" || got.GetParams()["cantidad"] != "2" {
		t.Errorf("params = %v, want producto/cantidad pobladas", got.GetParams())
	}
}

// El camino normal: la intención viaja sellada dentro de SensitivePayload.
func TestClassifiedIntent_RoundtripInSensitivePayload(t *testing.T) {
	in := &SensitivePayload{
		Text:     "quiero 2 panes integrales",
		PushName: "Cliente",
		FromPn:   "593999999999",
		Intent:   sampleIntent(),
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal SensitivePayload: %v", err)
	}
	var out SensitivePayload
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal SensitivePayload: %v", err)
	}
	if out.GetText() != in.GetText() {
		t.Errorf("text = %q, want %q", out.GetText(), in.GetText())
	}
	assertIntentEqual(t, out.GetIntent())
}

// El espejo en claro: la intención viaja en IncomingMessage.intent (solo para
// despliegues sin sealed transit).
func TestClassifiedIntent_RoundtripMirrorInIncomingMessage(t *testing.T) {
	in := &IncomingMessage{
		From:   "593999999999@s.whatsapp.net",
		Text:   "quiero 2 panes integrales",
		Intent: sampleIntent(),
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal IncomingMessage: %v", err)
	}
	var out IncomingMessage
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal IncomingMessage: %v", err)
	}
	assertIntentEqual(t, out.GetIntent())
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
