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

// legacyCloudToEdgeDescriptor construye el descriptor de un CloudToEdge previo al
// Plan 029: string command_id = 1; string session_id = 2; (sin el oneof ni el
// campo 15). Sirve para simular un receptor que no conoce config_update.
func legacyCloudToEdgeDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	lbl := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("legacy_cloudtoedge.proto"),
		Syntax:  proto.String("proto3"),
		Package: proto.String("wapp.cloudlink.legacy"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("CloudToEdge"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("command_id"), Number: proto.Int32(1), Type: &str, Label: &lbl, JsonName: proto.String("commandId")},
				{Name: proto.String("session_id"), Number: proto.Int32(2), Type: &str, Label: &lbl, JsonName: proto.String("sessionId")},
			},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("construir descriptor legacy: %v", err)
	}
	return fd.Messages().Get(0)
}
