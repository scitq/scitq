package lib

import (
	"context"
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

type QueueClient struct {
	conn   *grpc.ClientConn
	Client pb.TaskQueueClient
}

func authInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		md := metadata.Pairs("authorization", token)
		ctx = metadata.NewOutgoingContext(ctx, md)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// createClientConnection establishes a gRPC connection to the server.
func CreateLoginClient(serverAddr string) (QueueClient, error) {
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	conn, err := grpc.NewClient(
		serverAddr,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50<<20), // 50 MiB to match server
			grpc.MaxCallSendMsgSize(50<<20), // 50 MiB to match server
		),
	)
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

// createClientConnection establishes a gRPC connection to the server.
func CreateClient(serverAddr, token string) (QueueClient, error) {
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	conn, err := grpc.Dial(
		serverAddr,
		grpc.WithTransportCredentials(creds),
		grpc.WithUnaryInterceptor(authInterceptor(fmt.Sprintf("Bearer %s", token))),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50<<20), // 50 MiB to match server
			grpc.MaxCallSendMsgSize(50<<20), // 50 MiB to match server
		),
	)
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
