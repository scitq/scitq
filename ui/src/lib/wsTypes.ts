export type WSMessage = {
  type: string;    // 'job' | 'worker' | 'step' | 'step-stats' ...
  action: string;  // 'created' | 'updated' | 'deleted'
  id: number;      // entity id (jobId, workerId, etc.)
  payload: object; // always an object
};