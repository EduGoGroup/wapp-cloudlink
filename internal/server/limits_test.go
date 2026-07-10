package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-cloudlink/client"
	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"github.com/EduGoGroup/wapp-cloudlink/internal/server"
	"github.com/EduGoGroup/wapp-cloudlink/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// TestMessageSizeLimit cubre T7/H4: con los límites compartidos de transport
// aplicados en servidor y cliente, un mensaje que supera MaxMessageBytes se
// rechaza con ResourceExhausted (el cliente lo corta localmente por el límite de
// envío, espejo del límite de recepción del servidor).
func TestMessageSizeLimit(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer(transport.ServerOptions()...)
	cloudlinkv1.RegisterCloudLinkServer(gs, server.New())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialOpts := append([]grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, transport.DialOptions()...)

	cc, err := grpc.NewClient("passthrough:///bufnet", dialOpts...)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := client.New(ctx, cc)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	oversized := make([]byte, transport.MaxMessageBytes+1024)
	err = cli.Send(&cloudlinkv1.EdgeToCloud{
		SessionId: "s-big",
		Payload: &cloudlinkv1.EdgeToCloud_Incoming{
			Incoming: &cloudlinkv1.IncomingMessage{EncPayload: oversized},
		},
	})
	if err == nil {
		t.Fatal("mensaje > MaxMessageBytes: se esperaba error, got nil")
	}
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Fatalf("código gRPC: got %v, want ResourceExhausted (err=%v)", got, err)
	}
}
