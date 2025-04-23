import { TaskQueueClient } from '../../gen/taskqueue.client'; // Assurez-vous que le chemin est correct
import { GrpcWebFetchTransport } from '@protobuf-ts/grpcweb-transport';


export function getClient() {
  const transport = new GrpcWebFetchTransport({
    baseUrl: 'http://localhost:8081',
    fetchInit: {credentials: 'include'},
  });
  return new TaskQueueClient(transport);
}