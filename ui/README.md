# Svelte + TS + Vite + gRPC with Protobuf

This template helps you get started quickly with **Svelte**, **TypeScript**, and **Vite**, and integrates a backend via gRPC and Protobuf for managing worker data.

## 📁 Project Structure

The project is organized as follows:

ui/
├── gen/
│   ├── taskqueue.ts
│   ├── taskqueue.client.ts
│   └── google/
├── src/
│   ├── assets/
│   ├── components/
│   │   ├── createForm.svelte
│   │   ├── jobsCompo.svelte
│   │   ├── loginForm.svelte
│   │   ├── Sidebar.svelte
│   │   └── workerCompo.svelte
│   ├── lib/
│   │   ├── api.ts
│   │   └── grpcClient.ts
│   ├── pages/
│   │   ├── Dashboard.svelte
│   │   └── loginPage.svelte
│   ├── styles/
│   │   ├── createForm.css
│   │   ├── dashboard.css
│   │   ├── jobsCompo.css
│   │   ├── loginForm.css
│   │   ├── loginPage.css
│   │   └── worker.css
│   ├── App.svelte
│   ├── main.ts
│   └── app.css
├── index.html
└── package.json

> 📝 Notes:
> - The `gen/` folder contains TypeScript files generated from the `.proto` definitions.
> - `index.html`, `app.css`, and config files like `package.json` and `tsconfig.json` are located at the project root (`ui/`).
> - `styles/` only contains component-specific styles. The global `app.css` is at the root.

## Installation and Setup

### Prerequisites

Before you start, ensure you have the following tools installed:

- **Node.js** (version 16 or higher)
- **gRPC and Protobuf tools** to generate TypeScript files from `.proto` files.

### Installation Steps

1. Clone the project.
```bash
git clone https://github.com/gmtsciencedev/scitq2
cd ui
```

2. Install the dependencies:
```bash
npm install
```

3. Generate the TypeScript files from the .proto files:
```bash
npm run gen-proto
```
This command generates TypeScript files in the gen/ folder, allowing you to interact with the backend via gRPC.

4. Start the project in development mode:
```bash
npm run dev
```

### Generating TypeScript files from the .proto files:

This project uses gRPC and Protobuf for communication between the frontend and the backend. The .proto files define the structure of the messages and services used. From these definitions, you can automatically generate TypeScript files that are directly usable in the project.

#### ⚙️ Installing protoc
To generate the TypeScript files, you need to install the Protocol Buffers compiler (protoc) on your machine.

1. Download and Install
You can download the latest version of protoc from the official Protocol Buffers GitHub releases page.

Download the archive corresponding to your operating system.
Extract it and add the bin/ directory to your system's PATH environment variable.
```powershell
$env:Path += ";C:\Users\YourName\Downloads\protoc-21.x\bin"
```
```bash
export PATH="$PATH:/home/yourusername/Downloads/protoc-21.x/bin"
```

Then verify the installation:
```bash
protoc --version
```
You should see something like: libprotoc 3.21.x.

#### 📦 Generating the TypeScript Files
Once protoc is installed, the following command will generate the required .ts files from your .proto definitions:
```bash
npm run gen-proto
```
This script runs:
```bash
protoc --ts_out ./gen --proto_path=../proto taskqueue.proto
```
 - --ts_out ./gen: Specifies that the generated .ts files will be saved in the gen/ folder.
 - --proto_path=../proto: Points to the folder containing the .proto files.
 - taskqueue.proto: The Protobuf file used to describe services like workers and jobs.

The protoc-gen-ts plugin is already included in the project’s dev dependencies, so no extra installation is required.

#### 🗂 Output Files
Running the generation command creates two key files in the gen/ directory:

 - taskqueue.ts: Contains all the TypeScript types for the Protobuf messages (e.g., AddWorkerRequest, ListWorkersResponse).

 - taskqueue.client.ts: Provides a ready-to-use gRPC client with all service methods (e.g., addWorker(), listWorkers()).

These files can be imported and used directly in the application to interact with the backend via gRPC.

Additionally, if the .proto files use standard types like google.protobuf.Empty, a file like google/protobuf/empty.ts will also be generated automatically.

#### Important Dependencies

The following dependencies are required to enable gRPC and Protobuf integration:
 - protoc-gen-ts: Plugin to generate TypeScript types from Protobuf files.
 - grpc-web: Library to use gRPC in the browser.
 - @protobuf-ts/grpcweb-transport: Transport to handle gRPC requests via the grpc-web protocol.

Here are the main dependencies in your package.json:
```json
"devDependencies": {
  "@tsconfig/svelte": "^5.0.4",
  "protoc-gen-ts": "^0.6.0",
  "typescript": "~5.7.2",
  "vite": "^6.2.0"
},
"dependencies": {
  "@protobuf-ts/grpcweb-transport": "^2.9.6",
  "grpc-web": "^1.5.0",
  "google-protobuf": "^3.21.4"
}
```

## gRPC Client Integration and Communication
Inside the lib/grpcClient/ directory, a utility function is defined to create and configure the gRPC client.
To communicate with the backend services, this project uses **gRPC over HTTP via grpc-web**, allowing a Svelte + Vite frontend to interact seamlessly with a gRPC server — without the need for complex proxy setups.

### 🧠 Understanding the Role of lib/grpcClient
```ts
// src/lib/grpcClient.ts
import { TaskQueueClient } from '../../gen/taskqueue.client';
import { GrpcWebFetchTransport } from '@protobuf-ts/grpcweb-transport';

export function getClient() {
  const transport = new GrpcWebFetchTransport({
    baseUrl: 'http://localhost:8081', // Connects to gRPC-Web server
    fetchInit: { credentials: 'include' },
  });
  return new TaskQueueClient(transport);
}
```
This setup uses the @protobuf-ts/grpcweb-transport package to enable communication via gRPC-Web, a protocol that bridges traditional gRPC with browser environments (which don’t support HTTP/2 directly).

> ⚠️ **Important:** The `baseUrl` here (`http://localhost:8081`) corresponds to the gRPC-Web server — not the actual backend server.
>
> Typically, the backend exposes a gRPC server (on a port like `50051`), and a **gRPC-Web proxy server** (such as [Envoy](https://www.envoyproxy.io/) or [grpcwebproxy](https://github.com/improbable-eng/grpc-web)) listens on port `8081` to forward browser requests to it.
>
> This architecture **avoids the need for additional reverse proxies** (like Vite dev proxies), since requests from the frontend go straight to the gRPC-Web server.

### 🚀 Interacting with the gRPC Client via API Functions
In the lib/api.ts file, high-level functions encapsulate the client and expose methods that the UI can call.

These API utilities:

 - Abstract away the gRPC calls

 - Inject authentication when needed

 - Handle transformations and errors gracefully

Example – fetching the list of workers:
```ts
import { callOptions } from './auth';
import { getClient } from './grpcClient';
import * as taskqueue from '../../gen/taskqueue';

export async function getWorkers(): Promise<taskqueue.Worker[]> {
  try {
    const client = getClient();
    const result = await client.listWorkers(taskqueue.ListWorkersRequest, callOptions);
    return result.response?.workers || [];
  } catch (error) {
    console.error("Error fetching workers:", error);
    return [];
  }
}
```
Here:
 - getClient() provides the preconfigured gRPC client
 - callOptions injects an authentication token
 - taskqueue.ListWorkersRequest is a generated TypeScript object from .proto
 - The result is parsed and returned in a UI-friendly format

## 🛠 Features Enabled by gRPC APIs

The API layer (`lib/api.ts`) makes several key features possible:

| Feature           | Function(s)                                              | Description                                                    |
|-------------------|----------------------------------------------------------|----------------------------------------------------------------|
| 👤 Login          | `getLogin()`                                             | Authenticates a user via gRPC                                  |
| 👷 Worker Management | `getWorkers()`, `newWorker()`, `updateWorkerConfig()`, `delWorker()` | Lists, creates, updates, and deletes workers                   |
| 📋 Job Management  | `getJobs()`                                              | Retrieves job status and info                                  |
| 🧪 Flavor Discovery| `getFlavors()`                                           | Retrieves available flavors for worker creation                |
| 🎨 UI Mapping      | `getStatusText()`, `getStatusClass()`                   | Maps backend status codes to frontend classes                  |

These functions use the client generated from the `.proto` file (`taskqueue.client.ts`) and the associated data types (`taskqueue.ts`), making the communication **type-safe**, **predictable**, and **intuitive**.

## 🧩 Components

| 🧩 Component             | 📝 Description |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------|
| 🔨 **createForm.svelte** | Worker creation form with auto-completion (provider, flavor, region).<br>Uses `onMount()` to fetch available flavors.<br>Dynamic suggestion dropdown.<br>Calls `newWorker(...)` with form data.<br>**Styles**: `createForm.css` |
| 📋 **jobsCompo.svelte**  | Displays all current and past jobs with status, progress, and available actions.<br>Calls `getJobs()` in `onMount`.<br>Dynamic table display.<br>Lucide icons for restart/delete.<br>Status styled via `getStatusClass()` and `getStatusText()`.<br>**Styles**: `jobsCompo.css` |
| 🔐 **loginForm.svelte**  | Simple login form.<br>Uses `getClient().login()` for authentication.<br>Handles loading (`isLoading`) and errors.<br>**Styles**: `loginForm.css` |
| 📚 **Sidebar.svelte**    | Sidebar navigation with dropdowns and icons via lucide-svelte (Dashboard, Tasks, Batch, Settings, Logout).<br>Handles `tasksOpen` for submenus.<br>`isSidebarVisible` and `toggleSidebar()` passed as props.<br>**Styles**: `dashboard.css` |
| 👷 **workerCompo.svelte**| Displays all workers with metrics and modifiable actions.<br>Calls `getWorkers()` on `onMount`.<br>Buttons to change concurrency/prefetch (+/-).<br>Actions: edit, pause, delete (`delWorker()`).<br>Shows stats: CPU%, RAM, Load, Disk, Network.<br>**Styles**: `worker.css`, `jobsCompo.css` |

## 📄 Pages

| 📄 Page                  | 📝 Description |
|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 🖥️ **Dashboard.svelte**   | Main page after login.<br>Displays `WorkerCompo` (top), and `JobCompo` + `CreateForm` (bottom).<br>Includes hamburger button to toggle sidebar.<br>**Styles**: `dashboard.css` |
| 🔐 **loginPage.svelte**   | Login page with `LoginForm`.<br>Checks for token in `localStorage` and redirects to `/dashboard`.<br>Displays logo and header.<br>**Styles**: `loginPage.css` |


## 🧠 App.svelte – Root Component  
Handles login logic:

`isLoggedIn = true` ➜ Dashboard + Sidebar  
`false` ➜ Login Page

`toggleSidebar()` allows toggling the sidebar visibility.

Applies different CSS classes based on the mode (`body-dashboard`, `body-login`).


## 🧩 main.ts – Entry Point

Main file referenced in index.html.
Mounts the App.svelte component inside <div id="app"></div>.

## 🌍 index.html – HTML Entry

Single entry point of the app (Single Page Application).
Loads the main.ts script.

## 🔐 Authentication
Token is stored in localStorage.

Check is done on onMount inside loginPage.svelte.

If the token exists ➜ redirect to dashboard.

## 🔗 Libs & Dependencies

- `svelte`: Main UI framework (v5+)
- `vite`: Build tool and development server
- `lucide-svelte`: Icon library using Lucide SVGs
- `grpc-web`: Enables gRPC in the browser
- `google-protobuf`: JS runtime for protobuf types
- `@protobuf-ts/plugin`: Protobuf → TypeScript generator
- `@protobuf-ts/grpcweb-transport`: gRPC-Web transport layer
- `protoc-gen-ts`: CLI for generating TS from `.proto` files
- `rxjs`: Reactive utilities for async data
- `typedoc`: Generates documentation from TS comments
- `dotenv`: Loads env variables from `.env` file
- `svelte-check`: Type-checking and diagnostics for Svelte
- `@types/google-protobuf`: TypeScript types for `google-protobuf`

> These dependencies enable a full-featured Svelte app with type-safe gRPC communication, rich icon support, and automated documentation.

## Conclusion

This documentation helps you understand how to set up and use Svelte + Vite with a gRPC backend based on Protobuf. It covers the project structure, the TypeScript file generation tool, and how to manage gRPC services to interact with the server.
