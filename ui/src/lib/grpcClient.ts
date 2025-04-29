import { TaskQueueClient } from '../../gen/taskqueue.client'; // Assurez-vous que le chemin est correct
import { GrpcWebFetchTransport } from '@protobuf-ts/grpcweb-transport';

/**
 * Function to get the gRPC client for interacting with the TaskQueue service.
 * 
 * This function sets up a gRPC client using the `GrpcWebFetchTransport` with the specified base URL
 * and fetch options. It returns an instance of the `TaskQueueClient` that can be used to interact with
 * the backend service via gRPC.
 * 
 * @returns {TaskQueueClient} The gRPC client instance configured for the TaskQueue service.
 */
export function getClient() {
  const transport = new GrpcWebFetchTransport({
    baseUrl: 'http://localhost:8081',
    fetchInit: {credentials: 'include'},
  });
  return new TaskQueueClient(transport);
}
