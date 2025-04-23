import { callOptions } from './utils';
import { getClient } from './grpcClient';
import  * as taskqueue from '../../gen/taskqueue';

// Fonction pour récupérer les workers
export async function getWorkers(): Promise<taskqueue.Worker[]> {
    try {
        const client = getClient();
        const workerUnary = await client.listWorkers(taskqueue.ListWorkersRequest, callOptions);
        // Si le champ 'workers' existe directement dans la réponse
        return workerUnary.response?.workers || [];  // Si 'response.workers' existe
    } catch (error) {
        console.error("Erreur lors de la récupération des workers :", error);
        return [];  // Si une erreur se produit, assure-toi que workers est un tableau vide
    }
}

export async function updateWorkerConfig(workerId : any, newConcurrency: any, newPrefetch: any){
    try {
        const client = getClient();
        const allWorkers : taskqueue.Worker[] = await getWorkers();
        let targetWorker = null;

        for (const worker of allWorkers) {
          if (worker.workerId === workerId) {
                worker.concurrency = newConcurrency;
                worker.prefetch = newPrefetch;
                console.log("updated");
            break;
          }
        }

    } catch (error) {
        console.error("Update C&P error: ", error);
    }
}


// Fonction pour se login
export async function getLogin(username : string, password : string){
    try {
        const client = getClient();
        const response = client.login({ username, password });
        console.log(response);
    } catch (error) {
        console.error("Login error: ", error);
    }
}