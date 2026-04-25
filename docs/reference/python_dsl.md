# Main DSL API

## Functions

### `check_if_file(*uri: str)`

Check if all the URI provided points to a file

### `cond(*conditions: tuple[bool, any], default: any | None = None) -> <built-in function any>`

Switch-like function to assign a value to a variable on the first True condition.

Args:
    *conditions: Tuples of (condition, value) where condition is a boolean
                 and value is the value to assign if the condition is True.
                 If value is a callable (e.g. a lambda), it will be called
                 only when its condition is True, enabling lazy evaluation.
    default: Optional value to return if no conditions are True. If not provided,
             a ValueError will be raised if no conditions match.
             Can also be a callable for lazy evaluation.

Returns:
    A value based on the first True condition.

### `run(func: Callable, live: bool = False)`

Run a workflow function that may optionally take a Params class instance.

Behavior:
- --params: Outputs the parameter schema as JSON.
- --values: Parses values and runs the workflow.
- --metadata: Extracts workflow metadata (static AST inspection).
- --live: Keep the DSL running for dynamic task submission (optimization loops).
- No args:
    - If function takes no parameter, calls directly.
    - Otherwise, prints usage error.

When live=True (or --live flag), the workflow function receives a LiveContext
as its second argument: func(workflow, ctx) or func(params, workflow, ctx).


## Classes

## `LiveContext`

Provides wait/observe/stop/kill primitives for live DSL mode.

The LiveContext connects to a running scitq server and lets the DSL
script react to task results, submit new tasks, and control execution.
### `is_done(self, task) -> bool`

  Check if a task has reached terminal status.

### `kill(self, task)`

  Send SIGKILL (hard kill) to a running task.

### `observe(self, task) -> float | None`

  Non-blocking: return the current quality_score of a task, or None.

### `stop(self, task, grace_period: int | None = None)`

  Send SIGTERM (graceful stop) to a running task.
  grace_period: seconds before SIGKILL (default: 10).

### `wait(self, task) -> float | None`

  Block until a task reaches terminal status (S or F).
  
  Returns the quality_score on success, None if no quality defined.
  Raises RuntimeError on failure.
  When a task succeeds but quality_score is not yet populated (async extraction),
  polls a few more times to wait for it.

### `wait_all(self, tasks) -> List[Tuple[int, float | None]]`

  Wait for all tasks to reach terminal status.
  
  Returns list of (task_id, quality_score) pairs.
  Failed tasks have quality_score = None.
  For succeeded tasks, waits briefly for async quality extraction.

## `Outputs`

Represents the declarative outputs of a Step, which can be used in other Steps.
Note that the publish attribute is attached to Task so it may vary within a Step,
but not globs (e.g. named output), which should remain consistent for a given Step.

publish can be:
- a full URI string (e.g. "azure://rnd/results/project/") — used as-is
- True — uses the workflow's publish_root with auto-generated subpath
- a relative path (no "://") — appended to the workflow's publish_root
## `Param`

_No documentation available._
### `boolean(**kwargs)`

_No documentation available._

### `enum(choices: List[Any], **kwargs)`

_No documentation available._

### `integer(**kwargs)`

_No documentation available._

### `path(**kwargs)`

_No documentation available._

### `provider_region(**kwargs)`

_No documentation available._

### `string(**kwargs)`

_No documentation available._

## `ParamSpec`

type(object) -> the object's type
type(name, bases, dict, **kwds) -> a new type
### `parse(cls, values: dict)`

_No documentation available._

### `schema(cls)`

_No documentation available._

## `Python`

Abstract base class for workflow step languages (Shell, Raw, etc).
### `compile_command(self, command: str) -> str`

_No documentation available._

### `executable(self)`

_No documentation available._

## `Quality`

Defines quality variable extraction and scoring for a Step.

Variables are regex patterns with one capture group, applied to task stdout/stderr.

Single-objective (backward compatible):
    Quality(variables={"auc": r"auc: ([0-9.]+)"}, formula="auc")

Multi-objective:
    Quality(
        variables={"train_auc": r"train_auc: ([0-9.]+)", "test_auc": r"test_auc: ([0-9.]+)"},
        objectives=[
            {"formula": "test_auc", "direction": "maximize"},
            {"formula": "test_auc - train_auc", "direction": "maximize"},
        ],
    )
### `to_json(self) -> str`

_No documentation available._

## `Raw`

Default language: run the command as-is without shell injection or helpers.
### `compile_command(self, command: str) -> str`

_No documentation available._

### `executable(self) -> str | None`

_No documentation available._

## `Resource`

A resource to be used in a workflow step.

Attributes:
    path (str): The path to the resource.
    action (str): The action to perform with the resource, e.g., "untar".
## `Shell`

Abstract base class for workflow step languages (Shell, Raw, etc).
### `compile_command(self, command: str) -> str`

_No documentation available._

### `executable(self)`

_No documentation available._

## `TaskSpec`

_No documentation available._
## `URI`

A utility class to discover and group URIs (e.g. rclone resources specified in scitq style) from a remote source.
### `find(uri_base: str, group_by: str | None = None, pattern: str | None = None, filter: str | None = None, event_name: str | None = None, field_map: Dict[str, str] | None = None) -> Iterator[scitq2.uri.URIObject]`

  Discover and group URIs from a remote source.
  
  Args:
      uri_base: Base URI path to explore.
      group_by: 'folder', 'pattern.<group_name>', or None (group per file).
      pattern: Regex pattern (with named groups) for 'pattern' grouping. Named groups can either be used in event_name (group_by='pattern.<group_name>') or field_map (with file.pattern.<group_name>).
      filter: Glob expression for server-side filtering, e.g. '*.fastq.gz'.
      field_map: Output field name → expression (file.name, folder.name, file.pattern.<group_name>, etc)
      event_name: Name of the event to group by (e.g., 'folder.name' - default when group_by , 'file.name', 'file.pattern.name').
  
  Returns:
      Dict of event tag → URIObject

### `glob(uri_pattern: str) -> List[str]`

  Return a flat list of URIs matching a glob pattern.
  
  Example:
      files = URI.glob("azure://bucket/data/*.bam")

### `glob_groups(uri_pattern: str) -> List[scitq2.uri.URIObject]`

  Discover files and group them by folder.
  
  Returns a list of URIObject with 'name' (folder name) and 'uris' (list of matching URIs).
  
  Example:
      samples = URI.glob_groups("azure://bucket/data/*/*.fastq.gz")

## `W`

Static filter interface for worker recruitment.
This class provides a set of fields that can be used to filter workers
based on their attributes such as cpu, mem (memory), region, provider, flavor, and disk.
Usage:
worker_pool=WorkerPool(
    W.provider.like("azure%"),
    W.region.is_default(),
    W.cpu == 32,
    W.mem >= 120,
    W.disk >= 400,
    max_recruited=10,
    task_batches=2
)
## `WorkerPool`

Defines a strategy for recruiting workers based on hardware or metadata constraints.

A WorkerPool specifies:
- A list of filter expressions (e.g., W.cpu >= 32) that determine worker compatibility.
- Optional recruiter-level parameters such as `max_recruited`.
- Arbitrary extra options (e.g., container image, zone) passed to the backend.

NB: while filter expressions accept W.region and W.provider you should refrain to use those and
set region and provider at Workflow level to minimize inter-regional transfer fees. Yet you can still
use these filters notably when some specific instances are required but beware of transfer fees.

This class also supports estimating task concurrency based on a TaskSpec (cpu/mem needs).

Parameters:
    *match: FieldExpr
        Filter expressions to select eligible workers.
    max_recruited: int, optional
        Maximum number of workers this pool can recruit (per step).
    task_batches: int, optional
        Number of batches that each worker should do to complete the step workload
    timeout: int, optional
        Time (in seconds) to wait for a recyclable worker before resorting to creating a new one


Example:
    WorkerPool(
        W.cpu >= 32,
        max_recruited=10,
        task_batches=2
    )
### `build_recruiter(self, task_spec, default_provider: str | None = None, default_region: str | None = None) -> tuple[str, dict]`

_No documentation available._

### `clone_with(self, *match: scitq2.recruit.FieldExpr, max_recruited: int | None = None, task_batches: int | None = None, timeout: int | None = None) -> 'WorkerPool'`

  Returns a copy of the current WorkerPool with optionally overridden fields.
  Any provided arguments override the corresponding fields in the original pool.

### `compile_filter(self, default_provider: str | None = None, default_region: str | None = None) -> str`

  Compiles the worker pool's match expressions into a protofilter string.

## `Workflow`

Workflow objects are the corner stone of scitq DSL. They define the name, version and description of the template, 
as well as the defining settings for future scitq workflows created by the template. All Steps and Tasks are attached to the
Workflow and there can only be one Workflow object in a scitq DSL script.
- name: the template name (must be unique in scitq instance),
- version: enable to have different versions of the same template,
- description: a help text for users that shows in both UI and CLI,
- tag: a subname added (see naming_strategy) to the template name to create a workflow name that help distinguish each run (otherwise they are numbered),
- language: (default step value) language of commands in steps,
- worker_pool: definition of the workflow level worker pool (see WorkerPool objects), the workflow WorkerPool maximum_recruited define the workflow maximal recruitment,
- provider: instance provider ('local.local' for permanent workers, "azure.primary" or "openstack.ovh" for cloud workers, etc.),
- region: instance region (e.g. 'swedencentral' for Microsoft or 'GRA11' for OVH)
- naming_strategy: function to define how workflow and step names are constructed (default to dot_join),
- task_naming_strategy: function to define how task names are constructed (default to dot_join),
- container: (default step value) Docker container image for steps (can be overridden at step level),
- publish_root: base URI for publishing outputs (e.g. "azure://rnd/results/project/"). Steps can then use Outputs(publish=True) or Outputs(publish="subdir/"),
- resources: default resources for all steps (can be overridden at step level),
- retry: default number of retry for each task in the workflow (can be overridden at step level),
- run_strategy: workflow scheduling mode. Accepts a single-letter DB code ('B'/'T'/'D'/'Z') or a friendly word ('batch'/'thread'/'debug'/'suspended'). Default: server picks 'B' (batch). 'T' (thread) requests sticky scheduling — currently a partially-implemented feature: the field is plumbed through to the database, but the sticky-scheduling and worker-side I/O short-circuit logic is still pending (see specs/sticky_thread_run_strategy.md), so 'T' behaves like 'B' until that ships
### `Step(self, *, name: str, command: str, container: str | None = None, tag: str | None = None, inputs: str | scitq2.workflow.OutputBase | List[str] | List[scitq2.workflow.OutputBase] | None = None, outputs: scitq2.workflow.Outputs | None = None, resources: scitq2.uri.Resource | str | List[scitq2.uri.Resource] | List[str] | None = None, language: scitq2.language.Language | None = None, worker_pool: scitq2.recruit.WorkerPool | None = None, task_spec: scitq2.workflow.TaskSpec | None = None, naming_strategy: callable | None = None, depends: ForwardRef('Step') | List[ForwardRef('Step')] | None = None, skip_if_exists: bool | None = None, retry: int | None = None, accept_failure: bool = False, quality: scitq2.workflow.Quality | None = None) -> <function Workflow.Step at 0x10aea7cc0>`

  Add a Step to the Workflow with a single Task.
  If the Step already exists with the same name, the Task is added to the existing Step.
  - name: the step name,
  - command: the command to run in the task,
  - container: the container image to use for the task,
  - tag: an optional tag to distinguish multiple tasks within the same step,
  - inputs: optional inputs for the task (can be str, Output, or list of these),
  - outputs: optional Outputs object defining the task outputs,
  - resources: optional resources required for the task (can be Resource, str, or list of these),
  - language: optional language for the task command (overrides workflow default),
  - worker_pool: optional worker pool for the step (overrides workflow default),
  - task_spec: optional task specification for the step (overrides workflow default),
  - naming_strategy: optional naming strategy for the step (overrides workflow default),
  - depends: optional dependencies for the task (can be Step or list of Steps),
  - retry: optional number of retries for the task (overrides workflow default)

### `compile(self, client: scitq2.grpc_client.Scitq2Client, *, activate_leading_tasks: bool = False, workflow_status: str | None = None, opportunistic: bool = False, untrusted: List[str] | None = None) -> int`

_No documentation available._


# Biology extensions

## Functions

### `ENA(identifier: str, group_by: str, filter: scitq2.biology.SampleFilter | None = None, use_ftp: bool = False, use_aspera: bool = False, layout: str = 'AUTO') -> List[scitq2.biology.Sample]`

A Constructor of Sample objects extracted from a public project present in EMBL-EBI ENA https://www.ebi.ac.uk/ena/
- identifier: the project accession from which the Samples are created,
- group_by: how to group data information in what constitutes a base object, is the sample based upon the sample_accession, or the run_accession, or grouped by another variable,
- filter: See SampleFilter on how to use this,
- use_ftp: Force FTP transport in scitq URI (scitq provides sane defaults otherwise),
- use_aspera: Force Aspera transport in scitq URI (scitq provides sane defaults otherwise),
- layout: Specify layout (PAIRED/SINGLE) manually - if set to SINGLE, takes only r1 read if the real layout is PAIRED, default to AUTO (layout is inferred).

### `FASTQ(roots: Iterable[str] | str, *, group_by: str = 'folder', layout: Literal['AUTO', 'PAIRED', 'SINGLE'] = 'AUTO', only_read1: bool | None = None, strict_pairs: bool = False, allow_unknown: bool = True, study_vote: Literal['majority', 'all'] = 'majority', filter: str | None = None, pattern: str | None = None) -> List[scitq2.biology.Sample]`

High-level FASTQ source on top of URI.find().

Returns a list of sample dicts with keys:
  - sample_accession, project_accession
  - detected_layout: 'PAIRED' | 'SINGLE' | 'UNKNOWN'
  - library_layout: 'PAIRED' | 'SINGLE' (post-alignment)
  - reads: { 'R1': [...], 'R2': [...] } when effective_layout == 'PAIRED'
  - fastqs: list[str] (final selection after enforcement)

### `SRA(identifier: str, group_by: str, filter: scitq2.biology.SampleFilter | None = None, layout: str = 'AUTO', download_method: str = 'sra-aws') -> List[scitq2.biology.Sample]`

A Constructor of Sample objects extracted from a public project present in NIH NCBI SRA https://www.ncbi.nlm.nih.gov/sra
- identifier: the project accession from which the Samples are created,
- group_by: how to group data information in what constitutes a base object, is the sample based upon the sample_accession, or the run_accession, or grouped by another variable,
- filter: See SampleFilter on how to use this,
- layout: Specify layout (PAIRED/SINGLE) manually - if set to SINGLE, takes only r1 read if the real layout is PAIRED, default to AUTO (layout is inferred).
- download_method: specify the download method to use for FASTQ retrieval. Options are 'sra-tools' or 'sra-aws'. 

With SRA, transport is always sra-tools, maybe not the most performant but the most reliable tranport.

### `find_sample_parity(fastqs: List[str]) -> Dict[str, Any]`

classify a sample as PAIRED/SINGLE/UNKNOWN based on FASTQ names

### `try_float(s)`

_No documentation available._

### `with_properties(cls)`

_No documentation available._


## Classes

## `FieldBuilder`

_No documentation available._
### `isin(self, values)`

_No documentation available._

## `FieldExpr`

A object representing a Sample filtering condition. This should not be used directly, use SampleFilter together with an S expression

Example:
expr = S.library_strategy == "WGS"
## `S`

Static filter interface for ENA/SRA fields.
This class provides a set of fields that can be used to filter samples
based on their attributes such as run_accession, first_public, last_updated,
read_count, base_count, average_read_length, size_mb, experiment_accession,
library_name, library_strategy, library_selection, library_source, library_layout,
insert_size, instrument_platform, instrument_model, study_accession,
sample_accession, tax_id, scientific_name, sample_alias, and secondary_sample_accession.
Usage:
sample_filter = SampleFilter(
    S.library_strategy == "WGS",
    S.read_count >= 1000000,
    S.first_public >= "2020-01-01"
)
## `Sample`

_No documentation available._
### `is_empty(self)`

_No documentation available._

## `SampleFilter`

A SampleFilter object to filter Samples based on FieldExpr expressions.
Usage:
filter = SampleFilter(
    S.library_strategy == "WGS",
    S.read_count >= 1000000
)
samples = ENA(
    identifier="PRJNA123456",
    group_by="sample_accession",
    filter=filter
)

SampleFilter can use the following atributes from S:
- run_accession
- first_public
- last_updated
- experiment_accession
- library_name
- library_strategy
- library_selection
- library_source
- library_layout
- instrument_platform
- instrument_model
- study_accession
- sample_accession
- scientific_name
- sample_alias
- secondary_sample_accession
- insert_size
- tax_id
- read_count
- base_count
- nominal_length
- fastq_bytes
## `URI`

A utility class to discover and group URIs (e.g. rclone resources specified in scitq style) from a remote source.
### `find(uri_base: str, group_by: str | None = None, pattern: str | None = None, filter: str | None = None, event_name: str | None = None, field_map: Dict[str, str] | None = None) -> Iterator[scitq2.uri.URIObject]`

  Discover and group URIs from a remote source.
  
  Args:
      uri_base: Base URI path to explore.
      group_by: 'folder', 'pattern.<group_name>', or None (group per file).
      pattern: Regex pattern (with named groups) for 'pattern' grouping. Named groups can either be used in event_name (group_by='pattern.<group_name>') or field_map (with file.pattern.<group_name>).
      filter: Glob expression for server-side filtering, e.g. '*.fastq.gz'.
      field_map: Output field name → expression (file.name, folder.name, file.pattern.<group_name>, etc)
      event_name: Name of the event to group by (e.g., 'folder.name' - default when group_by , 'file.name', 'file.pattern.name').
  
  Returns:
      Dict of event tag → URIObject

### `glob(uri_pattern: str) -> List[str]`

  Return a flat list of URIs matching a glob pattern.
  
  Example:
      files = URI.glob("azure://bucket/data/*.bam")

### `glob_groups(uri_pattern: str) -> List[scitq2.uri.URIObject]`

  Discover files and group them by folder.
  
  Returns a list of URIObject with 'name' (folder name) and 'uris' (list of matching URIs).
  
  Example:
      samples = URI.glob_groups("azure://bucket/data/*/*.fastq.gz")

## `URIObject`

A grouped representation of one logical sample or event.

