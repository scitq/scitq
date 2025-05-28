import { vi } from 'vitest';
import * as api from '../lib/api';  // adapte le chemin

export const mockApi = {
  changepswd: vi.fn().mockResolvedValue(undefined),

  getListUser: vi.fn().mockResolvedValue([]),
  newUser: vi.fn().mockResolvedValue(42),
  delUser: vi.fn().mockResolvedValue(undefined),
  forgotPassword: vi.fn().mockResolvedValue(undefined),
  getUser: vi.fn().mockResolvedValue(null),
  updateUser: vi.fn().mockResolvedValue(undefined),

  getWorkers: vi.fn().mockResolvedValue([]),
  getStats: vi.fn().mockResolvedValue({}),

  getTasks: vi.fn().mockReturnValue({
    pending: vi.fn().mockResolvedValue(0),
    assigned: vi.fn().mockResolvedValue(0),
    accepted: vi.fn().mockResolvedValue(0),
    downloading: vi.fn().mockResolvedValue(0),
    running: vi.fn().mockResolvedValue(0),
    uploadingSuccess: vi.fn().mockResolvedValue(0),
    uploadingFailure: vi.fn().mockResolvedValue(0),
    succeeded: vi.fn().mockResolvedValue(0),
    failed: vi.fn().mockResolvedValue(0),
    suspended: vi.fn().mockResolvedValue(0),
    canceled: vi.fn().mockResolvedValue(0),
    waiting: vi.fn().mockResolvedValue(0),
  }),

  updateWorkerConfig: vi.fn().mockResolvedValue(undefined),

  getJobs: vi.fn().mockResolvedValue([]),
  delJob: vi.fn().mockResolvedValue(undefined),

  getFlavors: vi.fn().mockResolvedValue([]),
  getWorkFlow: vi.fn().mockResolvedValue([]),

  newWorker: vi.fn().mockResolvedValue([{ workerId: 1, workerName: 'worker-1' }]),
  delWorker: vi.fn().mockResolvedValue(undefined),

  getStatus: vi.fn().mockResolvedValue([]),

  getWorkerStatusClass: api.getWorkerStatusClass,
  getWorkerStatusText: api.getWorkerStatusText,
  getJobStatusClass: api.getJobStatusClass,
  getJobStatusText: api.getJobStatusText,
};

export default mockApi;
