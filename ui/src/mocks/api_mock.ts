import { vi } from 'vitest';
const api = await vi.importActual<typeof import('../lib/api')>('../lib/api');

// Every async export of the API (a server call) is a mock resolving to
// nothing; the synchronous ones (formatting, status labels) stay real. The
// entries below give calls whose callers expect a value a sensible default.
const auto = Object.fromEntries(Object.entries(api).map(([name, value]) => [
  name,
  typeof value === 'function' && value.constructor.name === 'AsyncFunction' ? vi.fn().mockResolvedValue(undefined) : value,
]));

export const mockApi: Record<string, any> = {
  ...auto,
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
  getJobStatus: vi.fn().mockResolvedValue([]),
  delJob: vi.fn().mockResolvedValue(undefined),

  getFlavors: vi.fn().mockResolvedValue([]),
  getWorkFlow: vi.fn().mockResolvedValue([]),

  newWorker: vi.fn().mockResolvedValue([]),
  delWorker: vi.fn().mockResolvedValue(undefined),

  getStatus: vi.fn().mockResolvedValue([]),
  getAllTaskStats: vi.fn().mockResolvedValue({ perWorkerStatusCounts: {}, globalStatusCounts: {}, totalCount: 0 }),
  listWorkerEvents: vi.fn().mockResolvedValue([]),


  getTasksCount: vi.fn().mockResolvedValue([]),
  getAllTasks: vi.fn().mockResolvedValue([]),
  streamTaskLogsOutput: vi.fn(),
  streamTaskLogsErr: vi.fn(),
  getLogsBatch: vi.fn().mockResolvedValue([]),
  
  getSteps: vi.fn().mockResolvedValue([]),
  getStepStats: vi.fn().mockResolvedValue([]),
  getRunningTasks: vi.fn().mockResolvedValue([]),
  listWorkers: vi.fn().mockResolvedValue([]),
  delStep: vi.fn().mockResolvedValue([]),
  delWorkflow: vi.fn().mockResolvedValue([]),

  getTemplates: vi.fn().mockResolvedValue([]),
  UploadTemplates: vi.fn().mockResolvedValue([]),
  runTemp: vi.fn().mockResolvedValue([]),


};

export default mockApi;
