import { callOptionsUserToken } from './auth';
import { client } from './grpcClient';
import * as taskqueue from '../../gen/taskqueue';

/* -------------------------------- USERS -------------------------------- */ 
 
/**
 * Retrieves a user based on the provided JWT token.
 *
 * @param tokenToFind - The JWT token containing userId or username.
 * @returns The matching user from the list or null if not found or invalid token.
 */
export async function getUser(tokenToFind: string): Promise<taskqueue.User | null> {
  // Decode the JWT
  const payload = parseJwt(tokenToFind);

  // Check if the payload is valid
  if (!payload || (!payload.userId && !payload.username)) {
    console.error("Invalid token or missing user information in token");
    return null;
  }

  // Retrieve the list of users
  const users = await getListUser();

  // Search for the user in the list
  const user = users.find(user =>
    (payload.userId && user.userId === payload.userId) ||
    (payload.username && user.username === payload.username)
  ) ?? null;

  if (!user) {
    console.error("User not found for token:", tokenToFind);
  }

  return user;
}

/**
 * Retrieves the list of users.
 *
 * @returns An array of users or an empty array if an error occurs.
 */
export async function getListUser(): Promise<taskqueue.User[]> {
  try {
    const jobUnary = await client.listUsers({}, await callOptionsUserToken());
    return jobUnary.response?.users || [];
  } catch (error) {
    console.error("Error while retrieving users:", error);
    return [];  // Return an empty array in case of error
  }
}

/**
 * Creates a new user.
 *
 * @param username - The username of the new user.
 * @param password - The password of the new user.
 * @param email - The email of the new user.
 * @param isAdmin - Whether the user is an admin.
 * @returns The ID of the created user or undefined if creation fails.
 */
export async function newUser(username: string, password: string, email: string, isAdmin: boolean): Promise<number | undefined> {
  try {
    const UserIdUnary = await client.createUser(
      { username, password, email, isAdmin },
      await callOptionsUserToken()
    );
    console.log("User created successfully!");
    return UserIdUnary.response.userId;
  } catch (error) {
    console.error("Error creating user:", error);
    return undefined;
  }
}

/**
 * Deletes a user by their ID.
 *
 * @param UserId - The ID of the user to delete.
 */
export async function delUser(UserId: number) {
  try {
    await client.deleteUser({ userId: UserId }, await callOptionsUserToken());
    console.log("User deleted successfully!");
  } catch (error) {
    console.error("Error deleting user: ", error);
  }
}

/**
 * Updates user information.
 *
 * @param userId - The ID of the user to update.
 * @param username - Optional new username.
 * @param email - Optional new email.
 * @param isAdmin - Optional new admin status.
 */
export async function updateUser(userId: number, updates: Partial<Omit<taskqueue.User, 'userId'>>) {
  try {
    await client.updateUser({ userId, ...updates }, await callOptionsUserToken());
  } catch (error) {
    console.error("Update user error:", error);
    throw error; // Important pour la gestion d'erreur dans le composant
  }
}
/**
 * Changes a user's password.
 *
 * @param username - The username of the user.
 * @param oldPassword - The current password.
 * @param newPassword - The new password.
 * @throws Will throw an error if the password update fails.
 */
export async function changepswd(username: string, oldPassword: string,newPassword: string) {
  try {
    await client.changePassword(
      { username, oldPassword, newPassword },
      await callOptionsUserToken()
    );
  } catch (error) {
    console.error("Update password error: ", error);
    throw new Error("Error. Please check that your password is valid.");
  }
}

/**
 * Resets a user's password by deleting and recreating the user.
 *
 * @param UserId - The user's ID.
 * @param username - The user's username.
 * @param password - The new password.
 * @param email - The user's email.
 * @param isAdmin - Whether the user is an admin.
 */
export async function forgotPassword(UserId: number, username: string, password: string, email: string, isAdmin: boolean) {
  try {
    await client.deleteUser({ userId: UserId }, await callOptionsUserToken());
    await client.createUser(
      { username, password, email, isAdmin },
      await callOptionsUserToken()
    );
    console.log("Password changed successfully!");
  } catch (error) {
    console.error("Error changing password: ", error);
  }
}

/**
 * Parses a JWT token and returns its payload.
 *
 * @param token - The JWT token to parse.
 * @returns The decoded payload object, or null if the format is invalid.
 */
function parseJwt(token: string): any {
  try {
    const base64Payload = token.split('.')[1];
    const payload = atob(base64Payload);
    return JSON.parse(payload);
  } catch (e) {
    console.error("Invalid token format", e);
    return null;
  }
}



/* -------------------------------- WORKERS -------------------------------- */ 

/**
 * Retrieves the list of workers.
 * @returns A promise resolving to an array of workers.
 */
export async function getWorkers(): Promise<taskqueue.Worker[]> {
  try {
    const workerUnary = await client.listWorkers({}, await callOptionsUserToken());
    return workerUnary.response?.workers || [];
  } catch (error) {
    console.error("Error while retrieving workers:", error);
    return [];
  }
}

/**
 * Retrieves stats for a list of worker IDs.
 * @param workerIds - The IDs of the workers.
 * @returns A record mapping worker IDs to their stats.
 */
export async function getStats(workerIds: number[]): Promise<Record<number, taskqueue.WorkerStats>> {
  try {
    const request: taskqueue.GetWorkerStatsRequest = { workerIds };
    const workerStatsUnary = await client.getWorkerStats(request, await callOptionsUserToken());
    const statsMap = workerStatsUnary.response?.workerStats ?? {};

    return workerIds.reduce((map, workerId) => {
      map[workerId] = statsMap[workerId] || {} as taskqueue.WorkerStats;
      return map;
    }, {} as Record<number, taskqueue.WorkerStats>);
  } catch (error) {
    console.error("Error while retrieving Stats:", error);
    return {};
  }
}

/**
 * Formats two byte values (used/total) into a human-readable string.
 * @param a - First value (e.g., used).
 * @param b - Second value (e.g., total).
 * @param decimals - Number of decimal places.
 * @returns Formatted string like '1.2/3.0 GB'.
 */
export function formatBytesPair(a: number | bigint, b: number | bigint, decimals = 1): string {
  a = typeof a === 'bigint' ? Number(a) : a;
  b = typeof b === 'bigint' ? Number(b) : b;

  if (isNaN(a) || isNaN(b)) return '0 / 0 B';

  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const max = Math.max(a, b);
  const i = Math.floor(Math.log(max) / Math.log(k));
  const unit = sizes[i];
  const dm = decimals < 0 ? 0 : decimals;

  const format = (val: number) => (val / Math.pow(k, i)).toFixed(dm);
  return `${format(a)}/${format(b)} ${unit}`;
}

/**
 * Updates a worker's configuration.
 * @param workerId - The ID of the worker.
 * @param concurrency - Optional concurrency level.
 * @param prefetch - Optional prefetch value.
 */
export async function updateWorkerConfig(workerId: number, updates: Partial<{ concurrency: number; prefetch: number }>) {
  try {
    await client.updateWorker({ workerId, ...updates }, await callOptionsUserToken());
  } catch (error) {
    console.error("Update Worker Config error:", error);
    throw error;
  }
}

/**
 * Retrieves available flavors.
 * @returns A promise resolving to an array of available flavors.
 */
export async function getFlavors(): Promise<taskqueue.Flavor[]> {
  try {
    const flavorUnary = await client.listFlavors({ limit: 1000, filter: `` }, await callOptionsUserToken());
    return flavorUnary.response?.flavors || [];
  } catch (error) {
    console.error("Error while retrieving flavors:", error);
    return [];
  }
}

/**
 * Creates new workers based on the given configuration.
 * @param concurrency - Number of concurrent tasks the worker can run.
 * @param prefetch - Number of tasks to prefetch.
 * @param flavor - Flavor name to use.
 * @param region - Region for deployment.
 * @param provider - Cloud provider.
 * @param number - Number of workers to create.
 * @param wfStep - Optional workflow.step string to associate a step.
 * @returns An array of created worker IDs and names.
 * @throws If the selected flavor or step is not found.
 */
export async function newWorker(
  concurrency: number,
  prefetch: number,
  flavor: string,
  region: string,
  provider: string,
  number: number,
  wfStep: string
): Promise<{ workerId: any; workerName: string }[]> {
  try {
    const listReq: taskqueue.ListFlavorsRequest = {
      limit: 1000,
      filter: ""
    };

    const flavorsListResponse = await client.listFlavors(listReq, await callOptionsUserToken());
    const flavorList = flavorsListResponse.response;
    const selectedFlavor = flavorList.flavors.find(f =>
      f.flavorName === flavor &&
      f.region === region &&
      f.provider === provider
    );

    if (!selectedFlavor) {
      throw new Error(`Flavor not found for: ${flavor} / ${region} / ${provider}`);
    }

    let stepId: number | null = null;
    if (wfStep) {
      const [workflowName, stepName] = wfStep.split(".");
      const workflows = await getWorkFlow(workflowName);
      const matchedWorkflow = workflows.find(wf => wf.name === workflowName);

      const stepListUnary = await client.listSteps(
        { workflowId: matchedWorkflow?.workflowId ?? 0 },
        await callOptionsUserToken()
      );
      const stepList = stepListUnary.response?.steps || [];
      const matchedStep = stepList.find(st => st.name === stepName);
      stepId = matchedStep?.stepId ?? null;
    }

    const workerReq: taskqueue.WorkerRequest = {
      concurrency,
      prefetch,
      flavorId: selectedFlavor.flavorId,
      regionId: selectedFlavor.regionId,
      providerId: selectedFlavor.providerId,
      number,
      ...(stepId !== null && { stepId })
    };

    const response = await client.createWorker(workerReq, await callOptionsUserToken());

    return response.response.workersDetails.map(w => ({
      workerId: w.workerId,
      workerName: w.workerName,
    }));
  } catch (error) {
    console.error("Error creating worker: ", error);
    throw error;
  }
}

/**
 * Delete a worker and return its deletion job ID
 * @param workerId Worker ID to delete
 * @returns ID of the deletion job or undefined if failed
 */
export async function delWorker(workerId: { workerId: any }) {
  try {
    const response = await client.deleteWorker({ workerId: workerId.workerId }, await callOptionsUserToken());
    console.log("Worker deleted successfully!");
    return response?.response.jobId;
  } catch (error) {
    console.error(`Error while deleting the worker: ${workerId.workerId}`, error);
  }
}

/**
 * Retrieves the count of tasks grouped by their status.
 * 
 * @param {number} [workerId] - Optional ID of the worker to filter tasks by.
 *                              If omitted, counts for all workers are returned.
 * @returns {Promise<Record<string, number>>} - A promise resolving to an object
 *                                             mapping task statuses to their counts.
 *                                             Includes a total count under the key 'all'.
 */
export async function getTasksCount(workerId?: number): Promise<Record<string, number>> {
  try {
    const request: taskqueue.ListTasksRequest = {};

    if (workerId) {
      request.workerIdFilter = workerId;
    }

    const taskUnary = await client.listTasks(request, await callOptionsUserToken());
    const workerTasks = taskUnary.response?.tasks || [];

    // Initialize counts for each status to zero
    const counts: Record<string, number> = {
      pending: 0,
      assigned: 0,
      accepted: 0,
      downloading: 0,
      running: 0,
      uploadingSuccess: 0,
      uploadingFailure: 0,
      succeeded: 0,
      failed: 0,
      suspended: 0,
      canceled: 0,
      waiting: 0,
      all: 0,
    };

    // Map status codes to keys in counts object
    const statusMap: Record<string, keyof typeof counts> = {
      P: 'pending',
      A: 'assigned',
      C: 'accepted',
      D: 'downloading',
      R: 'running',
      U: 'uploadingSuccess',
      V: 'uploadingFailure',
      S: 'succeeded',
      F: 'failed',
      Z: 'suspended',
      X: 'canceled',
      W: 'waiting',
    };

    // Count tasks per status
    for (const task of workerTasks) {
      const key = statusMap[task.status];
      if (key) {
        counts[key]++;
        counts.all++;
      }
    }

    return counts;

  } catch (error) {
    console.error("Error while retrieving worker tasks:", error);
    return {};
  }
}


/* -------------------------------- JOB -------------------------------- */ 

/**
 * Retrieves a paginated list of jobs from the server with optional limit and offset
 * 
 * @param {number} [limit] - Maximum number of jobs to return (for pagination)
 * @param {number} [offset] - Number of jobs to skip (for pagination)
 * @returns {Promise<taskqueue.Job[]>} Promise that resolves to an array of Job objects.
 *         Returns empty array if no jobs found or if an error occurs.
 * @throws {Error} Logs errors to console but returns empty array instead of throwing
 * 
 * @example
 * // Get first 10 jobs
 * const jobs = await getJobs(10);
 * 
 * @example
 * // Get next 10 jobs (pagination)
 * const jobs = await getJobs(10, 10);
 */
export async function getJobs(limit?: number, offset?: number): Promise<taskqueue.Job[]> {
    try {
      const request: taskqueue.ListJobsRequest = {};

      if (limit) {
        request.limit = limit;
      }
      if (offset) {
        request.offset = offset;
      }
      const jobUnary = await client.listJobs(request, await callOptionsUserToken());
      return jobUnary.response?.jobs || [];
    } catch (error) {
        console.error("Error while retrieving jobs:", error);
        return [];  // Return an empty array in case of error
    }
}

/**
 * Deletes a job by its ID.
 * 
 * @param jobId - An object containing the ID of the job to delete.
 * @returns A promise that resolves when the job is successfully deleted.
 */
export async function delJob(jobId: { jobId: any }) {
    try {
        await client.deleteJob({ jobId: jobId.jobId }, await callOptionsUserToken());
        console.log("Job deleted successfully!");
    } catch (error) {
        console.error(`Error while deleting the job: ${jobId.jobId}`, error);
    }
}

/**
 * Get statuses for multiple jobs
 * @param jobIds Array of job IDs to check
 * @returns Array of job statuses or empty array if error
 */
export async function getJobStatus(jobIds: number[]): Promise<taskqueue.JobStatus[]> {
  try {
    const response = await client.getJobStatuses({ jobIds }, await callOptionsUserToken());
    return response.response.statuses;
  } catch (error) {
    console.error('❌ Error fetching job statuses:', error);
    return [];
  }
}

/* -------------------------------- WORKFLOWS TEMPLATES -------------------------------- */ 

/**
 * Retrieves templates from the server with optional filtering
 * @param {number} [TemplateId] - Optional template ID to filter by
 * @param {string} [name] - Optional template name to filter by
 * @param {string} [version] - Optional version to filter by
 * @returns {Promise<taskqueue.Template[]>} Promise resolving to array of templates
 */
export async function getTemplates(TemplateId?: number, name?: string, version?: string): Promise<taskqueue.Template[]> {
  try {
    const requestParams: taskqueue.TemplateFilter = {};
    if (name) requestParams.name = name;
    if (TemplateId) requestParams.workflowTemplateId = TemplateId;
    if (version) requestParams.version = version;

    const tempUnary = await client.listTemplates(requestParams, await callOptionsUserToken());
    return tempUnary.response?.templates || [];
  } catch (error) {
    console.error("Error while retrieving templates:", error);
    return [];
  }
}

/**
 * Uploads a new template to the server
 * @param {Uint8Array} script - The template script/content to upload
 * @param {boolean} force - Whether to force upload if template exists
 * @returns {Promise<taskqueue.UploadTemplateResponse>} Promise resolving to upload response
 * @throws {Error} If upload fails
 */
export async function UploadTemplates(script: Uint8Array, force: boolean): Promise<taskqueue.UploadTemplateResponse> {
  try {
    const uplTempUnary = await client.uploadTemplate({ script, force }, await callOptionsUserToken());
    return uplTempUnary.response!;
  } catch (error) {
    console.error("Error while uploading templates:", error);
    throw error;
  }
}

/**
 * Executes a template with specified parameters
 * @param {number} workflowTemplateId - ID of the template to run
 * @param {string} paramValuesJson - JSON string of parameter values
 * @returns {Promise<taskqueue.TemplateRun>} Promise resolving to template run response
 * @throws {Error} If template execution fails
 */
export async function runTemp(workflowTemplateId: number, paramValuesJson: string): Promise<taskqueue.TemplateRun> {
  try {
    const runTempUnary = await client.runTemplate({ workflowTemplateId, paramValuesJson }, await callOptionsUserToken());
    return runTempUnary.response!;
  } catch (error) {
    console.error("Error while running templates:", error);
    throw error;
  }
}

/* -------------------------------- WORKFLOWS -------------------------------- */ 

/**
 * Retrieves a list of workflows from the server
 * @param {string} [name] - Optional name filter (case-insensitive partial match)
 * @param {number} [limit] - Optional pagination limit
 * @param {number} [offset] - Optional pagination offset
 * @returns {Promise<taskqueue.Workflow[]>} Promise resolving to array of workflows
 */
export async function getWorkFlow(name?: string, limit?: number, offset?: number): Promise<taskqueue.Workflow[]> {
  try {
    const request: taskqueue.WorkflowFilter = {};

    if (name) {
      request.nameLike = name;
    }
    if (limit) {
      request.limit = limit;
    }
    if (offset) {
      request.offset = offset;
    }
    const wfUnary = await client.listWorkflows(request, await callOptionsUserToken());
    return wfUnary.response?.workflows || [];
  } catch (error) {
    console.error("Error while retrieving workflows:", error);
    return [];
  }
}


/**
 * Deletes a workflow by their ID.
 *
 * @param WorkflowId - The ID of the workflow to delete.
 */
export async function delWorkflow(WorkflowId: number) {
  try {
    await client.deleteWorkflow({ workflowId: WorkflowId }, await callOptionsUserToken());
    console.log("Workflow deleted successfully!");
  } catch (error) {
    console.error("Error deleting workflow: ", error);
  }
}


/**
 * Retrieves all steps associated with a specific workflow
 * @param {number} workflowId - The workflow ID to get steps for
 * @param {number} [limit] - Optional pagination limit
 * @param {number} [offset] - Optional pagination offset
 * @returns {Promise<taskqueue.Step[]>} Promise resolving to array of steps
 */
export async function getSteps(workflowId: number, limit?: number, offset?: number): Promise<taskqueue.Step[]> {
  try {
    const request: taskqueue.StepFilter = {workflowId: workflowId};
    if (limit) {
      request.limit = limit;
    }
    if (offset) {
      request.offset = offset;
    }
    const stepUnary = await client.listSteps(request, await callOptionsUserToken());
    return stepUnary.response?.steps || [];
  } catch (error) {
    console.error("Error while retrieving steps:", error);
    return [];
  }
}


/**
 * Deletes a step by their ID.
 *
 * @param StepId - The ID of the step to delete.
 */

export async function delStep(StepId: number) {
  try {
    await client.deleteStep({ stepId: StepId }, await callOptionsUserToken());
    console.log("Step deleted successfully!");
  } catch (error) {
    console.error("Error deleting step: ", error);
  }
}

/**
 * Retrieves statistics for steps, optionally filtered by workflow, step IDs, and hidden inclusion.
 * @param params - Optional parameters for filtering stats:
 *   - workflowId: filter by workflow ID
 *   - stepIds: filter by specific step IDs
 *   - includeHidden: whether to include hidden steps
 * @returns Promise resolving to an array of StepStats
 */
export async function getStepStats(params?: {
  workflowId?: number;
  stepIds?: number[];
  includeHidden?: boolean;
}): Promise<taskqueue.StepStats[]> {
  try {
    const request: taskqueue.StepStatsRequest = { stepIds: [] };
    if (params?.workflowId !== undefined) {
      request.workflowId = params.workflowId;
    }
    if (params?.stepIds !== undefined) {
      request.stepIds = params.stepIds;
    }
    if (params?.includeHidden !== undefined) {
      request.includeHidden = params.includeHidden;
    }
    const response = await client.getStepStats(request, await callOptionsUserToken());
    return response.response?.stats || [];
  } catch (error) {
    console.error("Error while retrieving step stats:", error);
    return [];
  }
}



/* -------------------------------- TASKS -------------------------------- */ 

/**
 * Retrieves all tasks with optional filtering, sorting and pagination
 * @param {number} [workerId] - Filter by worker ID
 * @param {number} [workflowId] - Filter by workflow ID
 * @param {number} [stepId] - Filter by step ID
 * @param {string} [statusFilter] - Filter by status (e.g., 'pending', 'completed')
 * @param {'task' | 'worker' | 'workflow' | 'step'} [sortBy] - Field to sort by
 * @param {string} [command] - Filter by command name/type
 * @param {number} [limit] - Pagination limit
 * @param {number} [offset] - Pagination offset
 * @returns {Promise<taskqueue.Task[]>} Promise resolving to array of tasks
 */
export async function getAllTasks(
  workerId?: number,
  workflowId?: number,
  stepId?: number,
  statusFilter?: string,
  sortBy?: 'task' | 'worker' | 'workflow' | 'step',
  command?: string,
  limit?: number,
  offset?: number,
): Promise<taskqueue.Task[]> {
  try {
    const request: taskqueue.ListTasksRequest = {};

    if (statusFilter) {
      request.statusFilter = statusFilter;
    }
    if (workerId) {
      request.workerIdFilter = workerId;
    }
    if (workflowId) {
      request.workflowIdFilter = workflowId;
    }
    if (stepId) {
      request.stepIdFilter = stepId;
    }
    if (command) {
      request.commandFilter = command;
    }
    if (limit) {
      request.limit = limit;
    }
    if (offset) {
      request.offset = offset;
    }

    const taskUnary = await client.listTasks(request, await callOptionsUserToken());
    let allTasks = taskUnary.response?.tasks || [];

    // Apply sorting if specified
    if (sortBy && allTasks.length > 0) {
      allTasks = [...allTasks].sort((a, b) => {
        switch (sortBy) {
          case 'worker':
            return (a.workerId || 0) - (b.workerId || 0);
          case 'workflow':
            return (a.workflowId || 0) - (b.workflowId || 0);
          case 'step':
            return (a.stepId || 0) - (b.stepId || 0);
          default: // Default sorts by task ID
            return (b.taskId || 0) - (a.taskId || 0);
        }
      });
    }

    return allTasks;
  } catch (error) {
    console.error("Error while retrieving tasks:", error);
    return [];
  }
}

/**
 * Streams the standard output logs (`stdout`) of a task in real time.
 * Continuously listens to logs from the server and calls `onNewLog` for each received log entry.
 *
 * @param {number} taskIdNumber - The ID of the task for which logs should be streamed.
 * @param {(log: taskqueue.TaskLog) => void} onNewLog - Callback function that is invoked each time a new stdout log is received.
 * @returns {Promise<void>} A promise that resolves when the streaming ends or fails.
 */
export async function streamTaskLogsOutput(
  taskIdNumber: number,
  onNewLog: (log: taskqueue.TaskLog) => void
): Promise<void> {
  try {
    const TaskId: taskqueue.TaskId = { taskId: taskIdNumber };
    const taskLogStream = client.streamTaskLogsOutput(TaskId, await callOptionsUserToken());
    
    for await (const log of taskLogStream.responses) {
      onNewLog(log);
    }
  } catch (error) {
    console.error("Error while streaming task log:", error);
  }
}

/**
 * Streams the standard error logs (`stderr`) of a task in real time.
 * Continuously listens to logs from the server and calls `onNewLog` for each received error log entry.
 *
 * @param {number} taskIdNumber - The ID of the task for which error logs should be streamed.
 * @param {(log: taskqueue.TaskLog) => void} onNewLog - Callback function that is invoked each time a new stderr log is received.
 * @returns {Promise<void>} A promise that resolves when the streaming ends or fails.
 */
export async function streamTaskLogsErr(
  taskIdNumber: number,
  onNewLog: (log: taskqueue.TaskLog) => void
): Promise<void> {
  try {
    const TaskId: taskqueue.TaskId = { taskId: taskIdNumber };
    const taskLogStream = client.streamTaskLogsErr(TaskId, await callOptionsUserToken());
    
    for await (const log of taskLogStream.responses) {
      onNewLog(log);
    }
  } catch (error) {
    console.error("Error while streaming task log:", error);
  }
}

/**
 * Retrieves a batch of logs for specified tasks with pagination support
 * 
 * @async
 * @param {number[]} taskIds - Array of task IDs to fetch logs for
 * @param {number} chunkSize - Maximum number of log entries to return per task
 * @param {number} [skip] - Optional number of log entries to skip from the end (for pagination)
 * @param {string} [type] - Optional log type filter ('stdout' or 'stderr')
 * @returns {Promise<taskqueue.LogChunk[]>} Promise resolving to an array of log chunks, one per task
 * @throws Will not throw but returns empty array on error (errors are logged to console)
 * 
 * @example
 * // Get first 50 stdout logs for tasks 123 and 456
 * const logs = await getLogsBatch([123, 456], 50, 0, 'stdout');
 * 
 * @example
 * // Get next 50 stdout logs (skip first 50)
 * const moreLogs = await getLogsBatch([123, 456], 50, 50, 'stdout');
 */
export async function getLogsBatch(taskIds: number[], chunkSize: number, skip?: number, type?: string): Promise<taskqueue.LogChunk[]> {
  try {
    const request: any = {
      taskIds: taskIds,
      chunkSize: chunkSize,
    };

    if (skip !== undefined) {
      request.skipFromEnd = skip;
    }

    if (type !== undefined) {
      request.logType = type;
    }

    const response = await client.getLogsChunk(request, await callOptionsUserToken());
    return response.response?.logs || [];
  } catch (error) {
    console.error("Error while retrieving logs:", error);
    return [];
  }
}

/* -------------------------------- STATUS -------------------------------- */ 

/**
 * Retrieves the statuses of workers by their IDs.
 * 
 * @param workerIds - An array of worker IDs.
 * @returns A promise that resolves to an array of worker statuses.
 */
export async function getStatus(workerIds: number[]): Promise<taskqueue.WorkerStatus[]> {
  try {
    const response = await client.getWorkerStatuses({ workerIds }, await callOptionsUserToken());
    return response.response.statuses;
  } catch (error) {
    console.error('❌ Error while fetching worker statuses:', error);
    return []; // Return an empty list in case of error
  }
}

/**
 * Updates a worker's status via gRPC.
 * @param params - Object containing workerId and the new status code.
 */
export async function updateWorkerStatus(params: { workerId: number; status: string }): Promise<void> {
  try {
    const request: taskqueue.WorkerStatus = {
      workerId: params.workerId,
      status: params.status,
    };
    await client.updateWorkerStatus(request, await callOptionsUserToken());
  } catch (error) {
    console.error('❌ Error while updating worker status:', error);
    throw error;
  }
}

/**
 * Maps a job status code to a corresponding CSS class name.
 * 
 * @param status - The status code of the job.
 * @returns The CSS class name corresponding to the job status.
 */
export function getJobStatusClass(status: string): string {
  switch (status) {
    case 'P': return 'pending';
    case 'A': return 'assigned';
    case 'C': return 'accepted';
    case 'D': return 'downloading';
    case 'R': return 'running';
    case 'U': return 'uploading-success';
    case 'V': return 'uploading-failure';
    case 'S': return 'succeeded';
    case 'F': return 'failed';
    case 'Z': return 'suspended';
    case 'X': return 'canceled';
    case 'W': return 'waiting';
    default: return 'unknown';
  }
}

/**
 * Maps a job status code to a readable string.
 * 
 * @param status - The status code of the job.
 * @returns A human-readable string describing the job status.
 */
export function getJobStatusText(status: string): string {
  switch (status) {
    case 'P': return 'Pending';
    case 'A': return 'Assigned';
    case 'C': return 'Accepted';
    case 'D': return 'Downloading';
    case 'R': return 'Running';
    case 'U': return 'Uploading (success)';
    case 'V': return 'Uploading (failure)';
    case 'S': return 'Succeeded';
    case 'F': return 'Failed';
    case 'Z': return 'Suspended';
    case 'X': return 'Canceled';
    case 'W': return 'Waiting';
    default: return 'Unknown';
  }
}

/**
 * Maps a worker status code to a corresponding CSS class name.
 * 
 * @param status - The status code of the worker.
 * @returns The CSS class name corresponding to the worker status.
 */
export function getWorkerStatusClass(status: string): string {
  switch (status) {
    case 'O': return 'offline';
    case 'I': return 'installing';
    case 'R': return 'ready';
    case 'P': return 'paused';
    case 'F': return 'failing';
    case 'Q': return 'quarantined';
    case 'L': return 'lost';
    default: return 'unknown';
  }
}

/**
 * Maps a worker status code to a readable string.
 * 
 * @param status - The status code of the worker.
 * @returns A human-readable string describing the worker status.
 */
export function getWorkerStatusText(status: string): string {
  switch (status) {
    case 'O': return 'Offline';
    case 'I': return 'Installing';
    case 'R': return 'Ready';
    case 'P': return 'Paused';
    case 'F': return 'Failing';
    case 'Q': return 'Quarantined';
    case 'L': return 'Lost';
    default: return 'Unknown';
  }
}
