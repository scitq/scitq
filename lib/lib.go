package lib

import (
	"crypto/tls"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

type QueueClient struct {
	conn   *grpc.ClientConn
	Client pb.TaskQueueClient
}

// createClientConnection establishes a gRPC connection to the server.
func CreateClient(serverAddr string) (QueueClient, error) {
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(creds))
	qc := QueueClient{
		conn:   conn,
		Client: nil,
	}
	if err != nil {
		return qc, err
	}
	qc.Client = pb.NewTaskQueueClient(conn)
	return qc, err
}

// close client
func (qc *QueueClient) Close() {
	qc.conn.Close()
}
