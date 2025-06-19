import { TaskQueueClient } from '../../gen/taskqueue.client'; // Ensure the import path is correct
import { GrpcWebFetchTransport } from '@protobuf-ts/grpcweb-transport';

/**
 * Sets up the gRPC transport layer using GrpcWebFetchTransport.
 * Configured to use the base URL for the backend and include credentials with fetch requests.
 */
const transport = new GrpcWebFetchTransport({
  baseUrl: 'http://localhost:8081',
  fetchInit: { credentials: 'include' },
});

/**
 * Creates an instance of the TaskQueueClient using the configured gRPC transport.
 * This client is used to interact with the TaskQueue gRPC service.
 */
export const client = new TaskQueueClient(transport);
