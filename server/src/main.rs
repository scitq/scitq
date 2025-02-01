#![feature(inherent_associated_types)]

use rustls::pki_types::pem::PemObject;
use tonic::{Request, Response, Status};
use tonic::transport::{Server, Identity, ServerTlsConfig};
use tokio::sync::mpsc;
use tokio::net::TcpListener;
use tokio_rustls::rustls::{ServerConfig as RustlsServerConfig};
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use tokio_stream::{Stream, Empty, empty};

use tokio_rustls::TlsAcceptor;
use std::sync::{Arc, Mutex};
use std::collections::HashMap;
use chrono::{NaiveDateTime, Utc};
use serde::{Serialize, Deserialize, de::DeserializeOwned};
use sled;
use bincode;

use common::task_proto;
use task_proto::{
    task_queue_server::{TaskQueue, TaskQueueServer},
    TaskRequest, TaskResponse, WorkerInfo, Ack, TaskList,TaskStatusUpdate,
    TaskLog,
    TaskId,
};


/// A worker record stored in sled.
/// We use u64 for the generated ID.
#[derive(Serialize, Deserialize, Debug, Clone)]
struct Worker {
    id: u64,
    name: String,
    concurrency: i32,
}

/// A task record stored in sled.
/// The worker_id is optional (None means not assigned).
#[derive(Serialize, Deserialize, Debug, Clone)]
struct Task {
    id: u64,
    worker_id: Option<u64>,
    command: String,
    container: String,
    status: String, // e.g. "P" (pending), "A" (assigned), etc.
    created_at: Option<NaiveDateTime>,
}

/// The service handling task assignment and gRPC requests.
/// Note: Instead of a PgPool we now hold a sled::Db plus dedicated trees.
#[derive(Debug, Clone)]
struct TaskService {
    db: sled::Db,
    tasks: sled::Tree,
    workers: sled::Tree,
    // Additional logging channels (unchanged)
    log_channels: Arc<Mutex<HashMap<String, mpsc::Sender<task_proto::TaskLog>>>>,
}


/// Returns an iterator over deserialized items from a sled iterator.
/// Panics with a descriptive error message if iteration or deserialization fails.
fn iter_deserialized<T, I>(iter: I, context: &'static str) -> impl Iterator<Item = (sled::IVec, T)>
where
    T: DeserializeOwned,
    I: Iterator<Item = sled::Result<(sled::IVec, sled::IVec)>>,
{
    iter.map(move |item| {
        let (key, value) = item.unwrap_or_else(|e| {
            panic!("Failed iterating {}: {:?}", context, e)
        });
        let obj: T = bincode::deserialize(&value)
            .unwrap_or_else(|e| panic!("Failed to deserialize {} (key {:?}): {:?}", context, key, e));
        (key, obj)
    })
}


impl TaskService {
    /// Create a new TaskService by opening the sled database.
    /// We open two trees: one for tasks and one for workers.
    async fn new(path: &str) -> Self {
        // Open the sled database.
        let db = sled::open(path).unwrap_or_else(|e| {
            panic!("Failed opening store : {:?}", e)
        });
        // Open (or create) dedicated trees.
        let tasks = db.open_tree("tasks").unwrap();
        let workers = db.open_tree("workers").unwrap();
        Self {
            db,
            tasks,
            workers,
            log_channels: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    /// A background loop that periodically assigns pending tasks to workers.
    /// For each registered worker we count tasks already assigned (status "A", "C", or "R")
    /// and if below the concurrency limit, we scan the tasks tree for pending tasks (status "P")
    /// and update them to assigned ("A") with the worker's id.
    async fn assign_tasks(&self) {
        loop {
            // Iterate over all worker records, deserializing them.
            for (_wkey, worker) in iter_deserialized::<Worker,sled::Iter>(self.workers.iter(), "worker record") {
                // Count tasks assigned to this worker (status "A", "C", or "R").
                let assigned_count = iter_deserialized(self.tasks.iter(), "task record")
                    .filter(|(_, task) :&(sled::IVec, Task) | {
                        task.worker_id == Some(worker.id) && vec!["A","C","R"].contains(&task.status.as_str())
                    })
                    .count() as i32;

                if assigned_count < worker.concurrency {
                    let available = worker.concurrency - assigned_count;
                    // Collect pending tasks (status "P") up to the available capacity.
                    let pending_tasks: Vec<(sled::IVec, Task)> = iter_deserialized(self.tasks.iter(), "pending task record")
                        .filter(|(_, task) :&(sled::IVec, Task) | task.status == "P")
                        .take(available as usize)
                        .collect();

                    // For each pending task, update it to assigned ("A") with the worker's id.
                    for (tk, mut task) in pending_tasks {
                        let task_id = task.id;
                        task.status = "A".to_string();
                        task.worker_id = Some(worker.id);
                        let serialized = bincode::serialize(&task)
                            .unwrap_or_else(|e| panic!("Failed to serialize updated task (id {}) for key {:?}: {:?}", task_id, tk, e));
                        self.tasks.insert(tk.clone(), serialized)
                            .unwrap_or_else(|e| panic!("Failed to update task (id {}) for key {:?}: {:?}", task_id, tk, e));
                    }
                }
            }
            tokio::time::sleep(std::time::Duration::from_secs(5)).await;
        }
    }

}

#[tonic::async_trait]
impl TaskQueue for TaskService {
    /// submit_task receives a task from the client, assigns it an id,
    /// sets its status to pending ("P"), and stores it in the tasks tree.
    async fn submit_task(&self, request: Request<TaskRequest>) -> Result<Response<TaskResponse>, Status> {
        let task_req = request.into_inner();
        // Generate a new task ID.
        let id = self.db.generate_id().map_err(|e| Status::internal(format!("ID gen error: {}", e)))?;
        let new_task = Task {
            id,
            worker_id: None,
            command: task_req.command,
            container: task_req.container,
            status: "P".to_string(), // new tasks start as pending.
            created_at: Some(Utc::now().naive_utc()),
        };
        let serialized = bincode::serialize(&new_task).map_err(|e| Status::internal(format!("Serialization error: {}", e)))?;
        self.tasks.insert(&id.to_be_bytes(), serialized)
            .map_err(|e| Status::internal(format!("Sled error: {}", e)))?;
        
        Ok(Response::new(TaskResponse { task_id: id as i32 }))
    }

    /// register_worker adds (or updates) a worker record.
    /// Here we use a key formatted as "worker:<name>".
    async fn register_worker(&self, request: Request<WorkerInfo>) -> Result<Response<Ack>, Status> {
        let info = request.into_inner();
        let key = format!("worker:{}", info.name);
        // Check if the worker exists.
        if let Some(existing) = self.workers.get(key.as_bytes()).map_err(|e| Status::internal(format!("Sled error: {}", e)))? {
            // If found, update concurrency.
            let mut worker: Worker = bincode::deserialize(&existing)
                .map_err(|e| Status::internal(format!("Deserialization error: {}", e)))?;
            worker.concurrency = info.concurrency;
            let serialized = bincode::serialize(&worker)
                .map_err(|e| Status::internal(format!("Serialization error: {}", e)))?;
            self.workers.insert(key.as_bytes(), serialized)
                .map_err(|e| Status::internal(format!("Sled error: {}", e)))?;
        } else {
            // Otherwise, create a new worker record with a new generated ID.
            let id = self.db.generate_id().map_err(|e| Status::internal(format!("ID gen error: {}", e)))?;
            let worker = Worker {
                id,
                name: info.name.clone(),
                concurrency: info.concurrency,
            };
            let serialized = bincode::serialize(&worker)
                .map_err(|e| Status::internal(format!("Serialization error: {}", e)))?;
            self.workers.insert(key.as_bytes(), serialized)
                .map_err(|e| Status::internal(format!("Sled error: {}", e)))?;
        }
        Ok(Response::new(Ack { success: true }))
    }

    /// ping_and_take_new_tasks returns tasks assigned to a given worker.
    async fn ping_and_take_new_tasks(&self, request: Request<WorkerInfo>) -> Result<Response<TaskList>, Status> {
        let info = request.into_inner();
        let key = format!("worker:{}", info.name);
        // Lookup the worker record.
        let worker_bytes = self.workers.get(key.as_bytes())
            .map_err(|e| Status::internal(format!("Sled error: {}", e)))?
            .ok_or_else(|| Status::not_found("Worker not found"))?;
        let worker: Worker = bincode::deserialize(&worker_bytes)
            .map_err(|e| Status::internal(format!("Deserialization error: {}", e)))?;
        
        let mut tasks_vec = Vec::new();
        // Iterate over all tasks and filter those with status "A" assigned to this worker.
        for item in self.tasks.iter() {
            let (_tk, tv) = item.map_err(|e| Status::internal(format!("Sled error: {}", e)))?;
            let t: Task = bincode::deserialize(&tv)
                .map_err(|e| Status::internal(format!("Deserialization error: {}", e)))?;
            if t.status == "A" {
                if let Some(wid) = t.worker_id {
                    if wid == worker.id {
                        tasks_vec.push(t);
                    }
                }
            }
        }
        
        // Map tasks to the gRPC Task messages.
        let response_tasks = tasks_vec.into_iter().map(|t| task_proto::Task {
            task_id: t.id as i32,
            command: t.command,
            container: t.container,
            status: t.status
        }).collect();
        
        Ok(Response::new(TaskList { tasks: response_tasks }))
    }

    async fn update_task_status(
        &self, 
        request: Request<TaskStatusUpdate>
    ) -> Result<Response<Ack>, Status> {
        let update = request.into_inner();
        // For this example, simply print the update and (optionally) update your store.
        println!(
            "Received task status update for task {}: new status: {}",
            update.task_id,
            update.new_status
        );
        // (Optionally, update the corresponding task in sled.)
        Ok(Response::new(Ack { success: true }))
    }

    async fn send_task_logs(
        &self,
        request: Request<tonic::Streaming<TaskLog>>
    ) -> Result<Response<Ack>, Status> {
        let mut stream = request.into_inner();
        // Consume the log stream, printing each log.
        while let Some(log) = stream.message().await? {
            println!("Received task log: {:?}", log);
            // (Optionally, route the log to an internal channel, etc.)
        }
        Ok(Response::new(Ack { success: true }))
    }

    // Define the associated type for the server-streaming method.
    type StreamTaskLogsStream = Empty<Result<TaskLog, Status>>;

    async fn stream_task_logs(
        &self,
        request: Request<TaskId>
    ) -> Result<Response<Self::StreamTaskLogsStream>, Status> {
        let task_id = request.into_inner().task_id;
        println!("Received request to stream logs for task id: {}", task_id);
        // For now, we return an empty stream.
        let stream = empty();
        Ok(Response::new(stream))
    }


}


#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Set up the gRPC server address.
    let addr = "[::1]:50051".parse()?;
    
    // Open the sled database (here we use "sled.db").
    let task_service = Arc::new(TaskService::new("sled.db").await);
    
    // Load TLS certificates.
    //let certs = load_certs("server.pem")?;
    //let key = load_private_key("server.key")?;
        // Load TLS certificates.
    //let certs = CertificateDer::pem_file_iter("server.pem")?
    //    .collect::<Result<Vec<_>, _>>()?;
    //let key = PrivateKeyDer::from_pem_file("server.key")?;
    let cert = std::fs::read("server.pem")?;
    let key = std::fs::read("server.key")?;
    let identity = Identity::from_pem(cert, key);

//
    //let rustls_config = RustlsServerConfig::builder()
    //    .with_no_client_auth()
    //    .with_single_cert(certs, key)?;
    let tls_config = ServerTlsConfig::new().identity(identity);


    // Create your service instance.
    let task_service = TaskService::new("sled.db").await;
    
    println!("Server listening on {}", addr);
    
    // Build and run the server with TLS.
    Server::builder()
        .tls_config(tls_config) // This integrates TLS on the transport level.
        .expect("TLS configuration failed")
        .add_service(TaskQueueServer::new(task_service))
        .serve(addr)
        .await?;
        
    Ok(())
}
