// Package client implementa el lado edge del contrato CloudLink: abre el stream
// Connect, envía eventos edge->cloud y recibe comandos cloud->edge. La
// correlación comando<->ack se hace por command_id en capas superiores; aquí se
// expone el canal de recepción crudo. T2 es transporte vivo sin TLS.
package client

import (
	"context"
	"sync"

	cloudlinkv1 "github.com/EduGoGroup/wapp-cloudlink/gen/wapp/cloudlink/v1"
	"google.golang.org/grpc"
)

// Client mantiene el stream Connect abierto contra el cloud.
type Client struct {
	stream cloudlinkv1.CloudLink_ConnectClient

	sendMu   sync.Mutex
	received chan *cloudlinkv1.CloudToEdge
}

// New abre el stream Connect sobre una conexión gRPC ya establecida y arranca el
// loop de recepción. El ctx gobierna la vida del stream.
func New(ctx context.Context, cc grpc.ClientConnInterface) (*Client, error) {
	stream, err := cloudlinkv1.NewCloudLinkClient(cc).Connect(ctx)
	if err != nil {
		return nil, err
	}
	c := &Client{
		stream:   stream,
		received: make(chan *cloudlinkv1.CloudToEdge, 64),
	}
	go c.recvLoop()
	return c, nil
}

func (c *Client) recvLoop() {
	defer close(c.received)
	for {
		msg, err := c.stream.Recv()
		if err != nil {
			return
		}
		c.received <- msg
	}
}

// Send emite un evento/estado edge->cloud. Serializa los envíos: stream.Send no
// es seguro para uso concurrente.
func (c *Client) Send(msg *cloudlinkv1.EdgeToCloud) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(msg)
}

// Received expone los comandos cloud->edge recibidos. Se cierra cuando el stream
// termina (error o fin de contexto).
func (c *Client) Received() <-chan *cloudlinkv1.CloudToEdge {
	return c.received
}
