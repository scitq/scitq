import { callOptions } from './utils';
import { getClient } from './grpcClient';
import  * as taskqueue from '../../gen/taskqueue';


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

export async function updateWorkerConfig(workerId: any, concurrency?: any, prefetch?: any) {
    try {
      await getClient().updateWorker({workerId, concurrency, prefetch}, callOptions);
    } catch (error) {
      console.error("Update C&P error:", error);
    }
  }

// Fonction pour récupérer les flavors
export async function getFlavors(): Promise<taskqueue.Flavor[]> {
    try {
        const client = getClient();
        const flavorUnary = await client.listFlavors({limit: 1000,filter: ``}, callOptions);
        // Si le champ 'flavors' existe directement dans la réponse
        return flavorUnary.response?.flavors || [];  // Si 'response.flavors' existe
    } catch (error) {
        console.error("Erreur lors de la récupération des workers :", error);
        return [];  // Si une erreur se produit, assure-toi que workers est un tableau vide
    }
}

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
  
    //   const step = task.split(".")[1];
    //   const TaskListResponse = await client.listTasks(taskqueue.ListTasksRequest, callOptions);
    //   const taskList = TaskListResponse.response;
    //   const selectedTask = taskList.tasks.find(t => t.stepName === step);
  
    //   if (!selectedTask) {
    //     throw new Error(`Task not found for: ${step}`);
    //   }

  
    // Création de la requête WorkerRequest avec les informations du flavor trouvé
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


  export async function delWorker(workerId: { workerId: any }) {
    try {
        console.log("test 1");
        const client = getClient();

        // Assure-toi que workerId est de type uint32 (on prend juste le workerId)
        await client.deleteWorker({ workerId: workerId.workerId}, callOptions); 
        console.log("test 1");
    } catch (error) {
        console.error(`Erreur lors de la suppression du worker : ${workerId.workerId}`, error);
    }
}