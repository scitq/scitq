# Dev

## Code Structure
- **server/**: Handles gRPC requests and task management.
- **client/**: Implements worker logic.
- **cli/**: Provides command-line management tool.
- **lib/**: Contain common logic for client/cli for gRPC base client
- **cmd/**: Provides binaries for server, client, cli

## Task State Transitions
- **A** → Assigned
- **C** → Accepted
- **R** → Running
- **S** → Succeeded
- **F** → Failed

## Running Tests
```sh
go test ./...
```