import { callOptions } from './auth';
import { getClient } from './grpcClient';
import * as taskqueue from '../../gen/taskqueue';

/**
 * Function to log in a user.
 * 
 * @param username - The username of the user.
 * @param password - The password of the user.
 * @returns A promise that resolves when the login request is completed.
 */
export async function getLogin(username: string, password: string) {
    try {
        const client = getClient();
        const response = await client.login({ username, password });
        console.log(response);
    } catch (error) {
        console.error("Login error: ", error);
    }
}

/**
 * Function to retrieve the list of workers.
 * 
 * @returns A promise that resolves to an array of workers.
 */
export async function getWorkers(): Promise<taskqueue.Worker[]> {
    try {
        const client = getClient();
        const workerUnary = await client.listWorkers(taskqueue.ListWorkersRequest, callOptions);
        return workerUnary.response?.workers || [];  // If 'response.workers' exists
    } catch (error) {
        console.error("Error while retrieving workers:", error);
        return [];  // In case of error, return an empty array
    }
}

/**
 * Function to update the configuration of a worker.
 * 
 * @param workerId - The ID of the worker to be updated.
 * @param concurrency - The number of concurrent tasks the worker can handle.
 * @param prefetch - The number of tasks the worker should prefetch.
 * @returns A promise that resolves when the worker configuration is updated.
 */
export async function updateWorkerConfig(workerId: any, concurrency?: any, prefetch?: any) {
    try {
        await getClient().updateWorker({ workerId, concurrency, prefetch }, callOptions);
    } catch (error) {
        console.error("Update C&P error:", error);
    }
}

/**
 * Function to retrieve the list of jobs.
 * 
 * @returns A promise that resolves to an array of jobs.
 */
export async function getJobs(): Promise<taskqueue.Job[]> {
    try {
        const client = getClient();
        const jobUnary = await client.listJobs(taskqueue.ListJobsRequest, callOptions);
        console.log(jobUnary.response?.jobs);
        return jobUnary.response?.jobs || [];
    } catch (error) {
        console.error("Error while retrieving jobs:", error);
        return [];  // Return an empty array in case of error
    }
}

/**
 * Function to retrieve the list of flavors available.
 * 
 * @returns A promise that resolves to an array of flavors.
 */
export async function getFlavors(): Promise<taskqueue.Flavor[]> {
    try {
        const client = getClient();
        const flavorUnary = await client.listFlavors({ limit: 1000, filter: `` }, callOptions);
        return flavorUnary.response?.flavors || [];  // If 'response.flavors' exists
    } catch (error) {
        console.error("Error while retrieving flavors:", error);
        return [];  // In case of error, return an empty array
    }
}

/**
 * Function to create a new worker with the specified configuration.
 * 
 * @param concurrency - The number of concurrent tasks the worker can handle.
 * @param prefetch - The number of tasks the worker should prefetch.
 * @param flavor - The flavor to be used by the worker.
 * @param region - The region where the worker should be deployed.
 * @param provider - The provider for the worker.
 * @param number - The number of workers to be created.
 * @returns A promise that resolves when the worker is created.
 * @throws An error if the selected flavor is not found.
 */
export async function newWorker(concurrency: number, prefetch: number, flavor: string, region: string, provider: string, number: number) {
    try {
        const client = getClient();

        const listReq: taskqueue.ListFlavorsRequest = {
            limit: 1000,
            filter: ""
        };

        const flavorsListResponse = await client.listFlavors(listReq, callOptions);
        const flavorList = flavorsListResponse.response;
        const selectedFlavor = flavorList.flavors.find(f =>
            f.flavorName === flavor &&
            f.region === region &&
            f.provider === provider
        );

        if (!selectedFlavor) {
            throw new Error(`Flavor not found for: ${flavor} / ${region} / ${provider}`);
        }

        const workerReq: taskqueue.WorkerRequest = {
            concurrency,
            prefetch,
            flavorId: selectedFlavor.flavorId,
            regionId: selectedFlavor.regionId,
            providerId: selectedFlavor.providerId,
            number
        };

        await client.createWorker(workerReq, callOptions);
        console.log("Worker created successfully!");
    } catch (error) {
        console.error("Error creating worker: ", error);
    }
}

/**
 * Function to delete a worker by its ID.
 * 
 * @param workerId - The ID of the worker to be deleted.
 * @returns A promise that resolves when the worker is deleted.
 */
export async function delWorker(workerId: { workerId: any }) {
    try {
        const client = getClient();
        await client.deleteWorker({ workerId: workerId.workerId }, callOptions);
        console.log("Worker deleted successfully!");
    } catch (error) {
        console.error(`Error while deleting the worker: ${workerId.workerId}`, error);
    }
}

/**
 * Utility function to map worker status codes to CSS class names.
 * 
 * @param status - The status code of the worker.
 * @returns The corresponding CSS class name.
 */
export function getStatusClass(status: string): string {
    switch (status) {
        case 'P': return 'pending';
        case 'R': return 'running';
        case 'S': return 'succeeded';
        case 'F': return 'failed';
        case 'X': return 'canceled';
        default: return 'unknown';
    }
}

/**
 * Utility function to map worker status codes to readable status text.
 * 
 * @param status - The status code of the worker.
 * @returns The corresponding status text.
 */
export function getStatusText(status: string): string {
    switch (status) {
        case 'P': return 'Pending';
        case 'R': return 'Running';
        case 'S': return 'Succeeded';
        case 'F': return 'Failed';
        case 'X': return 'Canceled';
        default: return 'Unknown';
    }
}
