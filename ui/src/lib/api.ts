import { callOptionsWorkerToken, callOptionsUserToken } from './auth';
import { client } from './grpcClient';
import * as taskqueue from '../../gen/taskqueue';

let callOptionsWorker = await callOptionsWorkerToken();

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
    const jobUnary = await client.listUsers({}, callOptionsWorker);
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
      callOptionsWorker
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
    const workerUnary = await client.listWorkers(taskqueue.ListWorkersRequest, callOptionsWorker);
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
    const workerStatsUnary = await client.getWorkerStats(request, callOptionsWorker);
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
    await client.updateWorker(
      { workerId, ...updates },
      callOptionsWorker
    );
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
    const flavorUnary = await client.listFlavors({ limit: 1000, filter: `` }, callOptionsWorker);
    return flavorUnary.response?.flavors || [];
  } catch (error) {
    console.error("Error while retrieving flavors:", error);
    return [];
  }
}

/**
 * Retrieves workflows by name (if provided).
 * @param name - Optional name to filter workflows.
 * @returns A promise resolving to an array of workflows.
 */
export async function getWorkFlow(name?: string): Promise<taskqueue.Workflow[]> {
  try {
    const wfUnary = name
      ? await client.listWorkflows({ nameLike: name }, callOptionsWorker)
      : await client.listWorkflows({}, callOptionsWorker);
    return wfUnary.response?.workflows || [];
  } catch (error) {
    console.error("Error while retrieving workflows:", error);
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

    const flavorsListResponse = await client.listFlavors(listReq, callOptionsWorker);
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
        callOptionsWorker
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

    const response = await client.createWorker(workerReq, callOptionsWorker);

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
 * Deletes a worker by its ID.
 * @param workerId - Object containing the worker's ID.
 */
export async function delWorker(workerId: { workerId: any }) {
  try {
    await client.deleteWorker({ workerId: workerId.workerId }, callOptionsWorker);
    console.log("Worker deleted successfully!");
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

    const taskUnary = await client.listTasks(request, callOptionsWorker);
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
 * Retrieves the list of jobs.
 * 
 * @returns A promise that resolves to an array of jobs.
 */
export async function getJobs(): Promise<taskqueue.Job[]> {
    try {
        const jobUnary = await client.listJobs(taskqueue.ListJobsRequest, callOptionsWorker);
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
        await client.deleteJob({ jobId: jobId.jobId }, callOptionsWorker);
        console.log("Job deleted successfully!");
    } catch (error) {
        console.error(`Error while deleting the job: ${jobId.jobId}`, error);
    }
}


/* -------------------------------- TASKS -------------------------------- */ 

/**
 * Retrieves all tasks, optionally filtered by worker ID, step ID, and status.
 * Can also sort the resulting list by task ID, worker ID, or workflow step ID.
 * 
 * @param {number} [workerId] - Optional worker ID to filter tasks by.
 * @param {number} [stepId] - Optional step ID to filter tasks by.
 * @param {string} [statusFilter] - Optional task status to filter by.
 * @param {'task' | 'worker' | 'workflow'} [sortBy] - Optional sorting key for the returned tasks.
 * @returns {Promise<taskqueue.Task[]>} - A promise resolving to an array of tasks matching the filters and sorted if specified.
 */
export async function getAllTasks(
  workerId?: number,
  stepId?: number,
  statusFilter?: string,
  sortBy?: 'task' | 'worker' | 'workflow'
): Promise<taskqueue.Task[]> {
  try {
    const request: taskqueue.ListTasksRequest = {};

    if (statusFilter) {
      request.statusFilter = statusFilter;
    }

    if (workerId) {
      request.workerIdFilter = workerId;
    }

    const taskUnary = await client.listTasks(request, callOptionsWorker);
    let allTasks = taskUnary.response?.tasks || [];

    // Filter tasks by stepId if provided
    if (stepId !== undefined) {
      allTasks = allTasks.filter(task => task.stepId === stepId);
    }

    // Sort tasks if sortBy is provided
    if (sortBy) {
      allTasks.sort((a, b) => {
        switch (sortBy) {
          case 'task':
            return (a.taskId ?? 0) - (b.taskId ?? 0);
          case 'worker':
            return (a.workerId ?? 0) - (b.workerId ?? 0);
          case 'workflow':
            return (a.stepId ?? 0) - (b.stepId ?? 0);
          default:
            return 0;
        }
      });
    }

    return allTasks;
  } catch (error) {
    console.error("Error while retrieving tasks:", error);
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
    const response = await client.getWorkerStatuses({ workerIds }, callOptionsWorker);
    return response.response.statuses;
  } catch (error) {
    console.error('❌ Error while fetching worker statuses:', error);
    return []; // Return an empty list in case of error
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
