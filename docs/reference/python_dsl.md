# Main DSL API

## Functions

### `check_if_file(*uri: str)`

Check if all the URI provided points to a file

### `cond(*conditions: tuple[bool, any], default: Optional[<built-in function any>] = None) -> <built-in function any>`

Switch-like function to assign a value to a variable on the first True condition.

Args:
    *conditions: Tuples of (condition, value) where condition is a boolean
                 and value is the value to assign if the condition is True.
    default: Optional value to return if no conditions are True. If not provided,
             a ValueError will be raised if no conditions match.

Returns:
    A value based on the first True condition.

### `run(func: Callable)`

Run a workflow function that may optionally take a Params class instance.

Behavior:
- --params: Outputs the parameter schema as JSON.
- --values: Parses values and runs the workflow.
- --metadata: Extracts workflow metadata (static AST inspection).
- No args:
    - If function takes no parameter, calls directly.
    - Otherwise, prints usage error.


## Classes

## `Outputs`

Represents the declarative outputs of a Step, which can be used in other Steps.
Note that the publish attribute is attached to Task so it may vary within a Step, 
but not globs (e.g. named output), which should remain consistent for a given Step
## `Param`

_No documentation available._
### `boolean(**kwargs)`

_No documentation available._

### `enum(choices: List[Any], **kwargs)`

_No documentation available._

### `integer(**kwargs)`

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

## `Raw`

Default language: run the command as-is without shell injection or helpers.
### `compile_command(self, command: str) -> str`

_No documentation available._

### `executable(self) -> Optional[str]`

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

_No documentation available._
### `find(uri_base: str, group_by: Optional[str] = None, pattern: Optional[str] = None, filter: Optional[str] = None, event_name: Optional[str] = None, field_map: Optional[Dict[str, str]] = None) -> Iterator[scitq2.uri.URIObject]`

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
### `build_recruiter(self, task_spec, default_provider: Optional[str] = None, default_region: Optional[str] = None) -> tuple[str, dict]`

_No documentation available._

### `clone_with(self, *match: scitq2.recruit.FieldExpr, max_recruited: Optional[int] = None, task_batches: Optional[int] = None, timeout: Optional[int] = None) -> 'WorkerPool'`

  Returns a copy of the current WorkerPool with optionally overridden fields.
  Any provided arguments override the corresponding fields in the original pool.

### `compile_filter(self, default_provider: Optional[str] = None, default_region: Optional[str] = None) -> str`

  Compiles the worker pool's match expressions into a protofilter string.

## `Workflow`

_No documentation available._
### `Step(self, *, name: str, command: str, container: str, tag: Optional[str] = None, inputs: Union[str, scitq2.workflow.OutputBase, List[str], List[scitq2.workflow.OutputBase], NoneType] = None, outputs: Optional[scitq2.workflow.Outputs] = None, resources: Union[scitq2.uri.Resource, str, List[scitq2.uri.Resource], List[str], NoneType] = None, language: Optional[scitq2.language.Language] = None, worker_pool: Optional[scitq2.recruit.WorkerPool] = None, task_spec: Optional[scitq2.workflow.TaskSpec] = None, naming_strategy: Optional[<built-in function callable>] = None, depends: Union[ForwardRef('Step'), List[ForwardRef('Step')], NoneType] = None, retry: Optional[int] = None) -> scitq2.workflow.Step`

_No documentation available._

### `compile(self, client: scitq2.grpc_client.Scitq2Client) -> int`

_No documentation available._


# Biology extensions

## Functions

### `ENA(identifier: str, group_by: str, filter: Optional[scitq2.biology.SampleFilter] = None, use_ftp: bool = False, use_aspera: bool = False, layout: str = 'AUTO') -> List[scitq2.biology.Sample]`

_No documentation available._

### `FASTQ(roots: Union[Iterable[str], str], *, group_by: str = 'folder', layout: Literal['AUTO', 'PAIRED', 'SINGLE'] = 'AUTO', only_read1: Optional[bool] = None, strict_pairs: bool = False, allow_unknown: bool = True, study_vote: Literal['majority', 'all'] = 'majority', filter: Optional[str] = None, pattern: Optional[str] = None) -> List[scitq2.biology.Sample]`

High-level FASTQ source on top of URI.find().

Returns a list of sample dicts with keys:
  - sample_accession, project_accession
  - detected_layout: 'PAIRED' | 'SINGLE' | 'UNKNOWN'
  - library_layout: 'PAIRED' | 'SINGLE' (post-alignment)
  - reads: { 'R1': [...], 'R2': [...] } when effective_layout == 'PAIRED'
  - fastqs: list[str] (final selection after enforcement)

### `SRA(identifier: str, group_by: str, filter: Optional[scitq2.biology.SampleFilter] = None, layout: str = 'AUTO') -> List[scitq2.biology.Sample]`

_No documentation available._

### `find_sample_parity(fastqs: List[str]) -> Dict[str, Any]`

classify a sample as PAIRED/SINGLE/UNKNOWN based on FASTQ names

### `try_float(s)`

_No documentation available._

### `with_properties(cls)`

_No documentation available._


## Classes

## `Any`

Special type indicating an unconstrained type.

- Any is compatible with every type.
- Any assumed to have all methods.
- All values assumed to be instances of Any.

Note that all the above statements are true from the point of view of
static type checkers. At runtime, Any should not be used with instance
checks.
## `FieldBuilder`

_No documentation available._
### `isin(self, values)`

_No documentation available._

## `FieldExpr`

_No documentation available._
### `matches(self, record: Dict[str, Any]) -> bool`

_No documentation available._

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

_No documentation available._
### `matches(self, record: Dict[str, Any]) -> bool`

_No documentation available._

## `URI`

_No documentation available._
### `find(uri_base: str, group_by: Optional[str] = None, pattern: Optional[str] = None, filter: Optional[str] = None, event_name: Optional[str] = None, field_map: Optional[Dict[str, str]] = None) -> Iterator[scitq2.uri.URIObject]`

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

## `URIObject`

A grouped representation of one logical sample or event.

