# TODO — Next items

## skip-if-exists with glob patterns

Currently `skip_if_exists` calls `fetch.List` on the exact output path and checks if any files exist. This is too coarse — a partially failed task may have left files behind.

**Change**: accept glob patterns in the output path for skip-if-exists checks. Example: `azure://results/project/sample_A/*.tgz` — only skip if a `.tgz` file actually exists.

Requires: extending `fetch.List` to support glob filtering (it may already support this via rclone).

## Publish only on success

Currently task output is uploaded to the publish path regardless of task outcome (S or F). If skip-if-exists checks the published path, a failed task with partial output would be wrongly skipped on retry.

**Change**:
- On success (S): upload to publish path (as today)
- On failure (F): upload to workspace path only (for debugging), not to publish path

This makes skip-if-exists reliable: if output exists at the publish path, the task genuinely succeeded.

Requires: changes in the client upload logic (`client/uploader.go`) to check task status before choosing the upload destination.

## UI: Force run button for waiting tasks

Add a button in the task page UI to force a waiting (W) task to pending (P), bypassing dependency checks. Useful when some dependencies failed but you want partial results.

The server already supports this via `UpdateTaskStatus`. The UI just needs the button.
