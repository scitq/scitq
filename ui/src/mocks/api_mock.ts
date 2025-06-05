import { vi } from 'vitest';
import * as api from '../lib/api';

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
  updateWorkerConfig: vi.fn().mockResolvedValue(undefined),

  getJobs: vi.fn().mockResolvedValue([]),
  delJob: vi.fn().mockResolvedValue(undefined),

  getFlavors: vi.fn().mockResolvedValue([]),
  getWorkFlow: vi.fn().mockResolvedValue([]),

  newWorker: vi.fn().mockResolvedValue([]),
  delWorker: vi.fn().mockResolvedValue(undefined),

  getStatus: vi.fn().mockResolvedValue([]),

  getWorkerStatusClass: api.getWorkerStatusClass,
  getWorkerStatusText: api.getWorkerStatusText,
  getJobStatusClass: api.getJobStatusClass,
  getJobStatusText: api.getJobStatusText,
  formatBytesPair : api.formatBytesPair,

  getTasksCount: vi.fn().mockResolvedValue([]),
  getAllTasks: vi.fn(),

};

export default mockApi;
