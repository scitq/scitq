# Unified Versioned Module Library

## Status

Proposed. No code yet.

## Problem

Today scitq has two parallel, incompatible stores for YAML modules:

| | Bundled modules | Private modules |
|---|---|---|
| Location | `python/src/scitq2_modules/yaml/**/*.yaml` inside the `scitq2` Python package | `{script_root}/modules/*.yaml` on the server filesystem |
| Namespace | Path-based (`genomics/fastp`) | Flat, no slashes allowed (`filepath.Base(name) == name`) |
| Lifecycle | Shipped with the client release | Uploaded via `scitq module upload` |
| Versioning | Coupled to scitq package version | None — upload overwrites with `--force` |
| Resolved by | `_load_public_import` → reads from Python package on the worker running the runner | `_load_private_module` → searches pipeline dir + `{script_root}/modules` on the server |
| Updating | Wait for a scitq release → `pip install -U scitq2` everywhere | `scitq module upload --force` |

Concrete pains we hit:
- **A bundled-module bug fix needs a scitq release.** Example (recent): a `gzip -t` sanity check in `genomics/fastp.yaml` to catch truncated inputs. Today that change ships only when the whole package ships, on every worker's venv.
- **No site-specific overrides.** A lab wanting a different `fastp` adapter sequence must either fork the upstream module (losing upgradeability) or copy-paste into every template.
- **Private modules can't mirror the bundled taxonomy.** A team's internal aligner can't live at `metagenomics/my_aligner` next to `genomics/fastp`; it must be flat.
- **No history.** Overwriting a module silently changes every future template run that imports it. No way to pin a template to a specific module revision, and no audit trail.

## Goals

1. **One store, two origins.** Server-side module store holds both bundled and user-uploaded modules. `import: genomics/fastp` resolves the same way for both.
2. **Versioning.** Every module has an explicit `(path, version)` key. Existing templates keep working with whatever version was current at upload time.
3. **Origin provenance.** Each stored module knows whether it was seeded from the scitq package (`bundled`), uploaded by a user (`local`), or modified from bundled (`forked`).
4. **Path namespacing for private modules.** `scitq module upload --path metagenomics/my_aligner.yaml` works and is resolvable as `import: metagenomics/my_aligner`.
5. **Admin-controlled updates.** Admin decides when to pull newer bundled modules; forks are never clobbered silently.
6. **Offline / dev compatibility.** The `scitq2_modules` Python package remains usable as a fallback for local dry-runs without a server.

## Non-goals

- Three-way merge UIs for reconciling forks against upstream. `diff` + manual edit is sufficient.
- Curated category enforcement. No required `genomics/` or `metagenomics/`, no allowed-prefix list. Folders are organisational, not mandatory.
- Dependency resolution between modules. Templates still reference modules directly; modules don't import other modules (that's already the case today).
- **Python modules (`scitq2_modules.fastp` and friends).** See [Why YAML only](#why-yaml-only) below. Python modules stay in the pip-installed `scitq2_modules` package.

## Why YAML only

YAML templates are *always executed server-side* (the CLI ships the YAML bytes to the server; the server's `yaml_runner` subprocess resolves `import:`, builds the workflow, submits tasks). Any YAML-module resolution hook lives in the server, so a server-side store is both sufficient and authoritative.

Python-DSL templates are symmetric: a user can run `python my_pipeline.py` on their laptop, in which case `from scitq2_modules import fastp` executes before any interaction with the scitq server and is resolved against whatever `scitq2_modules` the laptop's pip has installed. The server has no hook to inject a different version at that point. If we kept a server-side store of Python modules, it would only apply when the template is executed by the server's subprocess — not when it's executed locally. That kind of "works on the server, works differently on your laptop" split is a debugging nightmare.

So Python modules stay where they are today: in the `scitq2_modules` Python package, updated via `pip install -U scitq2` (or pinning to a fork / internal index for site-specific patches). Versioning is pip's job. If a user wants a reusable piece of Python logic, they write a normal Python module and distribute it like any other Python package.

This makes the module library a YAML-only store. If a future need for versioned Python modules arises — for instance, a "template runs exclusively server-side" flag that could justify server-supplied Python modules — it can be layered on later without touching this spec.

## Core concepts

### Identity

A YAML module is identified by the pair `(path, version)` where:

- `path` — forward-slash path in a virtual namespace, no leading slash, no `..`, no empty segments. Examples: `genomics/fastp`, `metagenomics/bowtie2_host_removal`, `internal/my_aligner`, `hello`.
- `version` — tolerant semver: a leading `MAJOR.MINOR.PATCH` followed by an optional `-<suffix>`. Examples: `1.2.3`, `1.1.1-site`, `2.0.0-beta1`. Ordering: numeric MAJOR.MINOR.PATCH first, then suffix lexicographic, then a row without suffix sorts higher than one with (so `1.1.0` > `1.1.0-rc1`). New uploads MUST bump at least PATCH.

A "resolvable name" in a template is `path[@version]`:

```yaml
steps:
  - import: genomics/fastp                 # latest available
  - import: genomics/fastp@latest          # same — explicit form
  - import: genomics/fastp@1.2.0           # pinned to a concrete version
```

`@latest` is a magic value meaning "highest-ordered version present in the store at resolution time". Bare `path` (no `@`) is shorthand for `@latest`. This mirrors `workflow_template`'s use of `latest`. `latest` is never stored — the runner always records the resolved concrete version into `template_run.module_pins`, so a re-run with those pins replays exactly what ran the first time even if a newer version has since been uploaded.

`latest` is also a **reserved version string**: uploads with `--version latest` are rejected (would be ambiguous).

### Origin

Every module row has an `origin`:

- `bundled` — seeded from the scitq package, never modified locally. Content hash matches the package's shipped hash.
- `forked` — originated from `bundled` but diverged locally (admin edited `genomics/fastp` to add a site-specific adapter). Never overwritten by sync without `--force`.
- `local` — uploaded by a user, no bundled counterpart.

Origin is per-row (per version). Bumping a forked module to a new version resets the decision point: the next sync of bundled can either upsert a new bundled version alongside the fork (both live) or be blocked until admin resolves — see [Sync semantics](#sync-semantics).

### Resolution

**One keyword, one source.** When a template uses `import: <path>[@<version>]`:

1. Server looks up `(path, version)` in the `module` table. If `version` is omitted, the highest-version row wins.
2. Not found → hard error: `module '<path>'[@<version>]' not found in library. Run 'scitq module upgrade --apply' to seed bundled modules.` The runner does **not** fall back to the Python package, the script root, or anywhere else.

Key property: **the library is the single source of truth at runtime.** Admins can therefore:

- exclude or delete a bundled module and trust it is gone (no package fallback re-introduces it),
- upload a private module at any namespace and have templates reach it the same way as bundled content,
- forget about on-disk `{script_root}/modules/` entirely — it is no longer consulted.

Templates become univocal: every `import:` means "library lookup", full stop. There is no user-facing distinction between bundled and private modules at template-authoring time.

### Opt-in offline mode

For development and dry-run scenarios that must run without a server, `python -m scitq2.yaml_runner` accepts:

- `--offline` — bypass the server entirely. Module resolution reads from a filesystem tree instead.
- `--yaml-module-path DIR` — in offline mode, use `DIR` as the root of the module tree (e.g. `DIR/genomics/fastp.yaml`). Defaults to the installed `scitq2_modules/yaml/` directory in the current Python environment.

Without `--offline`, the runner always talks to the server and fails loud if the server is unreachable. There is no implicit offline fallback — the behavior is explicit and visible at the command line. This is deliberate: offline is a testing tool, not a degradation of normal operation. Pointing `--yaml-module-path` at a work-in-progress tree (e.g. a git checkout) makes local iteration on bundled modules tractable without round-tripping through the server.

The `--offline` flag is rejected on the server-side run path (the yaml_runner subprocess started by `RunTemplate` is always online); it is only meaningful when the user runs `yaml_runner` directly from the CLI.

### Unified `import:`, deprecated `module:`

The `module:` keyword is retained as a deprecated alias of `import:` for one release cycle. Concretely:

- A template using `module: X.yaml` emits a one-line deprecation warning on each run: `⚠️ 'module:' is deprecated; use 'import:' with the library path (e.g. 'import: private/X')`.
- During the deprecation window, `module:` still resolves against the library exactly like `import:`, with one compatibility shim: a trailing `.yaml` on the ref is stripped before lookup (so `module: biomscope_align.yaml` resolves to path `biomscope_align` in the library).
- After the deprecation window, `module:` is removed. Templates must use `import:`.

Migrating private modules from `{script_root}/modules/` into the library is a one-time admin step: `scitq module upload --path /scripts/modules/X.yaml --as private/X` (or whatever namespace the admin chooses). After that, templates can reference `import: private/X`.

## Server-side storage

### Two-tier layout: filesystem canonical + DB index

Module content is stored on disk; the `module` table is a metadata index that can be rebuilt from the filesystem. Rationale:

- **Config-shaped content belongs on disk.** Modules are declarative YAML — git-trackable, rsync-backable, admin-inspectable by `cat`/`grep`. Everything else that's config-shaped in scitq (templates, scitq.yaml, provider configs) already lives on disk for the same reasons.
- **DB loss is recoverable.** If the PostgreSQL database is wiped, a server restart reindexes from the filesystem and the library returns to working order. Backups can focus on the one directory; the DB catches up by itself.
- **Operational state stays in DB.** Workflows, tasks, template_runs, recruiters — everything genuinely transient/transactional — remains where it belongs. The DB keeps its operational role; it just stops being the authoritative store for module bytes.

**Directory layout** (rooted at `scitq.modules_root`, default `/var/lib/scitq/modules`):

```
/var/lib/scitq/modules/
├── genomics/
│   ├── fastp/
│   │   ├── 1.0.0.yaml
│   │   └── 1.1.0.yaml
│   └── multiqc/
│       └── 1.0.0.yaml
├── metagenomics/
│   └── meteor2/
│       └── 1.0.5.yaml
└── private/
    └── biomscope_align/
        └── 1.0.0.yaml
```

Filename is exactly `<version>.yaml`; directory prefix is the module's namespace path. Writes are atomic (temp file + rename) so a crash mid-write leaves either the old file or no file at that path, never a partial.

### Schema

```sql
CREATE TABLE module (
    module_id    SERIAL PRIMARY KEY,
    path         TEXT NOT NULL,            -- e.g. 'genomics/fastp' (no leading slash, no extension)
    version      TEXT NOT NULL,            -- tolerant semver, see Identity
    content_sha  TEXT NOT NULL,            -- sha256 hex of the on-disk content, for change detection
    origin       CHAR(1) NOT NULL CHECK (origin IN ('B','L','F')),  -- Bundled / Local / Forked
    bundled_sha  TEXT,                     -- sha256 of the bundled content this row derives from (NULL for 'L')
    uploaded_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    uploaded_by  INTEGER REFERENCES scitq_user(user_id),
    description  TEXT,                     -- optional, extracted from YAML 'description:' if present
    UNIQUE (path, version)
);

CREATE INDEX module_path_idx ON module (path);
```

Notes:
- **No `content` column.** The bytes live on disk at `{modules_root}/<path>/<version>.yaml`. The row is a pure metadata index.
- `content_sha` lets the sync job detect "has the bundled version on disk changed since I last seeded it" without having to re-read the file.
- `bundled_sha` on a `forked` row records the upstream content it diverged from — powers the "your fork is N versions behind bundled" diagnostic.
- No soft-delete. Modules accumulate; templates pin what they used. Old versions can be pruned via an explicit `scitq module prune <path> --keep 5` if storage becomes a concern (out of scope for v1).

### Write-side invariants

Every operation that adds or updates a module writes the file first, then the metadata row. If the file write succeeds and the DB update fails, the file remains consistent and a subsequent reindex will insert the missing metadata row automatically. If the file write fails, the DB is untouched — no phantom rows.

- `UploadModule` — `writeModuleFile(...)` then `INSERT/UPDATE module`.
- `UpgradeBundledModules` / `autoUpgradeBundledModules` — same.
- `ForkModule` — `readModuleFile` of the source, `writeModuleFile` at the new version, then `INSERT module` for the fork row.
- `importLegacyOnDiskModules` — same (writes the legacy file to the new layout before inserting).

### Read-side invariants

Every operation that returns module bytes reads from disk; the DB row provides the `(path, version, origin, content_sha, …)` metadata only.

- `DownloadModule` — look up the row, then read `{modules_root}/<resolved_path>/<resolved_version>.yaml`.
- `ListModulesFiltered` / `GetModuleOrigin` — metadata-only, no disk read.

If a metadata row exists but the file is missing or unreadable, `DownloadModule` surfaces a clear error pointing at the reindex/re-upload recovery path. This can only happen if someone manually deletes files from `{modules_root}` without going through the `scitq module delete` flow, or via out-of-band disk corruption.

### Startup sequence

On every server startup, after PostgreSQL migrations have applied:

1. **Content-to-disk migration.** For each row with `content IS NOT NULL` (legacy schema), write the bytes to `{modules_root}/<path>/<version>.yaml` and clear the column. Idempotent — after the first successful pass, subsequent startups find zero matching rows and the step no-ops.
2. **Legacy on-disk import.** For each file in `{script_root}/modules/*.yaml`, write it to the canonical `{modules_root}/<name>/<version>.yaml` location and insert a metadata row as `origin=L`. One-off migration from the pre-library layout; idempotent.
3. **Reindex.** Walk `{modules_root}` for any `.yaml` file not already indexed in the `module` table. Insert a metadata row with `origin=B` for paths under `genomics/` and `metagenomics/` (the shipped-bundled namespaces), `origin=L` otherwise. Handles DB-loss recovery: a fresh DB on top of an existing filesystem rebuilds the index automatically.
4. **Auto-upgrade bundled modules** (async, gated on Python venv readiness). See "Auto-upgrade at server startup" above.

The first three phases are synchronous and block startup until complete. The fourth is async because it needs the Python venv to be ready; meanwhile the server starts serving requests using whatever is already in the library.

### Seeding bundled modules

Bundled content lives in the installed `scitq2_modules` Python package at `scitq2_modules/yaml/<path>.yaml`, each file carrying a `version:` field (required going forward on bundled modules).

Triggered by `scitq module upgrade` (admin only) **and automatically at every server startup** (configurable, enabled by default — see Auto-upgrade below):

```
for each bundled YAML file discovered under scitq2_modules/yaml/:
    path    = relative path with .yaml stripped (e.g. 'genomics/fastp')
    version = YAML 'version:' field (REQUIRED)
    sha     = sha256(content)

    find row where (path, version):
        if not found:
            insert as origin=B, bundled_sha=sha
        elif origin=B and content_sha != sha:
            # same (path, version) re-shipped with different bytes —
            # packaging bug on our side, refuse and log (admin can --force)
        elif origin=F:
            # fork at this exact version — leave alone; report in dry-run
        else:
            # origin=B, content_sha matches — no-op

    # The package shipping a NEW version for an existing path just inserts
    # a new (path, version) row as origin=B. Older forks and older
    # bundled rows remain; list_modules --versions shows the ladder.
```

Writes only. Never deletes rows, even if a bundled entry is removed from the package (keep reproducibility of old template_runs). An admin-visible warning surfaces in `module list` for any `origin=B` row whose path no longer exists in the package.

The `scitq2_modules` Python package therefore remains the source of truth for bundled content; `module upgrade` copies it into the server store. This is a deliberate choice — the alternative (embedding a tarball in the server binary) is more packaging work and doesn't help the common case, which is "admin installed a new scitq release, wants the new bundled modules available to templates".

### Auto-upgrade at server startup

Because the library is now the single source of truth, a fresh install would not resolve any bundled `import:` until someone runs `scitq module upgrade --apply`. To keep "it just works" as the default, the server runs `module upgrade --apply` semantics automatically at every startup, subject to configuration.

Configuration block in `scitq.yaml`:

```yaml
scitq:
  autoupgrade_modules: true           # default: true
  autoupgrade_exclude:                # default: []
    - "metagenomics/*"                # glob: single-segment wildcard
    - "genomics/multiqc"              # exact path
```

Startup behaviour when enabled:

1. Walk `scitq2_modules/yaml/` in the installed package.
2. For each file, compute `path` and match against `autoupgrade_exclude`. Skip matches.
3. For the rest, apply the same upsert rules as `module upgrade --apply`:
   - `(path, version)` absent → insert as `origin=B`.
   - `(path, version)` present with matching SHA → no-op.
   - `(path, version)` present with different SHA → log warning, skip (manual `--force` still required for an in-place overwrite).
   - Any `origin=F` row at the path → skip. Forks are never overwritten by auto-upgrade.
4. Log a one-line summary: `auto-upgrade: inserted N, matched M, excluded K, preserved L forks`. Quiet for pure no-op runs.

Glob syntax for `autoupgrade_exclude`: `*` matches any single path segment (so `metagenomics/*` matches every direct child but not nested subpaths); `**` matches any number of segments recursively; everything else is treated as literal. Exact paths (no wildcard) match exactly.

Disabling (`autoupgrade_modules: false`):

- Nothing runs at startup. Templates requesting bundled modules that haven't been manually upgraded will fail.
- Admins can still `scitq module upgrade [--apply]` manually to produce the same result on demand.
- Appropriate for frozen-library environments (audit-sensitive sites, CI) or when the admin wants to gate every update through a review step.

Interaction with an explicitly-deleted module: `autoupgrade_exclude` prevents re-import on subsequent startups but does **not** retroactively remove rows. To both delete and prevent reinstatement, admin issues `scitq module delete <ref>` (see CLI) then adds the path to `autoupgrade_exclude`.

### Forking semantics

A `local` upload at a path that already has a `bundled` row at a different version: just a `local` row. No special handling.

A `local` upload at a path that already has a `bundled` row **at the same version**: rejected. You can't collide with bundled for the same `(path, version)`. Bump the version.

Admin edits a bundled module via the CLI/MCP edit path (see below):
- The row's `origin` flips to `forked`, its `bundled_sha` is set to the pre-edit content hash, and the content updates in place (not a new version — this is the "I'm patching my deployment of this exact version" case).
- An explicit fork-as-new-version path is also available: `scitq module fork genomics/fastp@1.1.0 --new-version 1.1.1-site` creates a new `forked` row at `(genomics/fastp, 1.1.1-site)` without touching the original.

### Sync semantics

`scitq module upgrade` (admin-only) walks the installed `scitq2_modules/yaml/` tree and applies the seeding logic above. Dry-run by default, `--apply` to commit. Output:

```
$ scitq module upgrade
genomics/fastp                     1.2.0   bundled (new)         would insert
genomics/fastp                     1.1.0   forked                keep (local edits detected)
metagenomics/bowtie2_host_removal      1.0.3   bundled (up-to-date)  no-op
internal/my_aligner               2.0.0   local                 no-op

2 new bundled modules, 1 fork preserved, 0 conflicts.
Re-run with --apply to write.
```

Conflicts (extremely rare — a re-shipped bundled version at the same `(path, version)` with different content) are reported and skipped unless `--force`.

## CLI / MCP surface

### CLI

All commands operate on YAML modules. Python modules are not part of this library (see [Why YAML only](#why-yaml-only)).

```sh
# Upload. --path is the local file; server-side namespace taken from --as,
# defaulting to the basename of --path with .yaml stripped.
scitq module upload --path ./modules/my_aligner.yaml                 # path=my_aligner
scitq module upload --path ./modules/my_aligner.yaml --as metagenomics/my_aligner

# Explicit version; if omitted, read from YAML 'version:'.
scitq module upload --path ./genomics/fastp.yaml --version 1.2.0 --as genomics/fastp

# Download. --version defaults to latest available.
scitq module download --name genomics/fastp
scitq module download --name genomics/fastp --version 1.1.0 -o fastp-1.1.0.yaml

# List. Flat by default, --tree groups by folder, --versions shows all versions per path.
scitq module list
scitq module list --tree
scitq module list --versions genomics/fastp

# Show provenance: origin, hash, who uploaded.
scitq module origin genomics/fastp
scitq module origin genomics/fastp@1.1.0

# Create a local fork of a bundled module at a new version.
scitq module fork genomics/fastp@1.1.0 --new-version 1.1.1-site

# Admin-only: seed / update bundled modules from the installed scitq2 package.
scitq module upgrade                # dry-run, shows diff
scitq module upgrade --apply
```

### MCP

Extend existing tools, no new categories:

- `upload_module(path: str, content: bytes, version: str, force: bool = false)` — `path` now accepts slashes.
- `download_module(name: str, version: str | None = null)`
- `list_modules(tree: bool = false, versions: bool = false, path: str | None = null)`
- new: `module_origin(name: str, version: str | None = null)`
- new: `fork_module(source_path: str, source_version: str, new_version: str)` (admin only)
- new admin tool: `upgrade_modules(apply: bool = false)`

### Server gRPC

Extend `UploadModule`, `DownloadModule`, `ListModules`; add `ModuleOrigin`, `ForkModule`, `UpgradeBundledModules`. Existing flat-name requests keep working — server treats a request with no slash identically to a path of the same name.

## Runner integration

`_load_public_import` and `_load_private_module` collapse into one function, `_load_module(import_name, version=None)`:

```
if --offline flag is set:
    root = --yaml-module-path or installed scitq2_modules/yaml/
    return read(root/<path>.yaml)   # version ignored in offline mode
else:
    rpc DownloadModule(path=import_name, version=version) → content
    if not found → hard error with hint to run 'scitq module upgrade --apply'
```

Since YAML templates always execute server-side in production (the CLI ships YAML bytes to the server; the server's `yaml_runner` subprocess resolves the imports), the server-path is what runs in practice. `--offline` is a testing and development switch exposed only on direct CLI invocation of `yaml_runner`.

### `{RESOURCE_ROOT}` hard-fail

Orthogonal to the library but enforced by the same runner: if a template references `{RESOURCE_ROOT}` anywhere (scan covers `resource:`, `publish:`, `command:`, `vars:`, recursively) and the runner cannot resolve it to a real value — no `workspace:` declared, workspace doesn't map to a `provider:region`, no `local_resources` entry for that pair, or the server lookup fails — the run is **rejected before any workflow is created**, with the specific reason logged to stderr. The prior silent-expansion-to-empty-string behaviour is gone. Templates that don't reference `{RESOURCE_ROOT}` are unaffected.

### Pin recording

When a template runs through the YAML runner, for each `import: <path>` without an explicit version, the runner records the `(path, version)` actually resolved and writes it into `template_run.module_pins` (JSONB array). Re-running a stored `template_run` replays with the exact same module content even after a later `module upgrade`.

## Template pinning

Templates do not need to pin versions by default. Two escape hatches for when pinning matters:

- **Explicit pin in YAML**: `import: genomics/fastp@1.1.0`. The runner respects it.
- **Recorded resolution** (above): a template_run snapshots the versions it actually used. A "reproduce this run" flow can replay with the same pins even if the template YAML is unversioned.

New templates that care about reproducibility are expected to pin manually. scitq does not force pinning.

## Migration

Migration happens in two stages, both handled automatically at server startup. Admins do not need to run anything manually.

### From pre-library (on-disk at `{script_root}/modules/`)

On the first startup of a library-aware server:

1. `CREATE TABLE module (...)` as above (via the SQL migration).
2. For each file in `{script_root}/modules/*.yaml`, write the content to `{modules_root}/<name>/<version>.yaml` and insert a metadata row as `origin=L`. `version` comes from the YAML's `version:` field, falling back to `0.0.0`.
3. Reindex `{modules_root}` for anything already on disk not yet in the DB.
4. Auto-upgrade bundled modules from the installed `scitq2_modules` package.

The legacy `{script_root}/modules/` directory is not deleted — it is simply no longer consulted at runtime. An admin can remove it manually once confident the migration ran cleanly.

### From library-v1 (content stored in DB)

For servers upgraded from an earlier library-aware release that stored content in the `module.content` BYTEA column:

1. SQL migration 23 relaxes `NOT NULL` on `content`.
2. Server startup runs `migrateModuleContentToDisk`: for each row with `content IS NOT NULL`, write the bytes to `{modules_root}/<path>/<version>.yaml` and clear the column. Idempotent.
3. A follow-up SQL migration in a later release drops the `content` column entirely. In the interim it stays nullable and zero-filled — no data hazard.

### Backup

After migration, the two things worth backing up are:

- `{modules_root}` — the authoritative content.
- PostgreSQL — for workflow state, templates, users, etc. The `module` table in particular is recoverable from `{modules_root}` via startup reindex, so it's the weaker of the two dependencies.

Loss of PostgreSQL: rebuild from backups, let the server reindex. Loss of `{modules_root}`: must restore from backup — the DB index alone can't reconstruct content. Loss of both: re-run `scitq module upgrade --apply` to get the bundled modules back, re-upload any private/forked modules from the admin's own git history or backup.

### Client runner changes

For clients (the Python yaml_runner): all library access is now RPC-based. Offline dry-run via `--offline` continues to work by reading from the filesystem directly. No breaking change to the on-wire gRPC surface.

## Future improvements

Out of scope for v1, but worth noting so the design doesn't paint us into a corner:

- **Auto-prune of unreferenced bundled rows.** Over many `module upgrade` cycles the store accumulates old bundled versions. A retention policy ("drop `origin=B` rows older than N months with no `template_run.module_pins` reference, and no newer version in a fork chain that depends on them") keeps size bounded without breaking reproducibility. Deserves its own spec (`specs/module-retention.md`) covering reference-counting, grace periods, and admin overrides.
- **Server-only Python-DSL templates.** A flag on `workflow_template` that marks a template as non-runnable client-side would unblock layering Python modules on top of this store in a follow-up spec — the "client executes, server-store has no hook" problem disappears when the client is never the executor. Not needed for this spec; just flagging that the schema leaves room by keeping identity at `(path, version)` without a `kind` column (adding one later is a simple ALTER).

## Test plan

- Unit: seeding skips matching bundled rows, inserts new ones, preserves forks. Colliding content at same `(path, version)` refused without `--force`.
- Unit: upload with slashes accepted; upload with `..` or leading `/` rejected.
- Unit: version ordering handles `1.2.0` > `1.1.1-site` > `1.1.1-rc1` > `1.1.0` correctly.
- Integration: upload private `metagenomics/my_aligner`, import from a template, verify it loads. Upload a `local` at a path bundled already has: both visible, version-specific imports resolve the right one.
- Integration: admin edits bundled `genomics/fastp` via CLI; row flips to `forked`; `module upgrade` dry-run reports it as fork-preserved; `--apply` does not overwrite.
- Integration: pre-migration server with on-disk private modules is upgraded; all existing private modules appear in the new table with `origin=L` and their content lives at `{modules_root}/<name>/<version>.yaml`; templates that imported them still resolve.
- Integration: library-v1 server with content in DB is upgraded; `migrateModuleContentToDisk` writes each row to `{modules_root}` and clears the column; `DownloadModule` serves the same bytes pre- and post-migration.
- Integration: DB-loss recovery — drop the `module` table with the filesystem tree intact, restart; reindex rebuilds every metadata row, `module list` and `DownloadModule` return the same content.
- Filesystem: crash-mid-write safety — manually kill the server after a temp file is created but before the rename; restart; the module is either fully present or absent, never partial.
- Offline: `python -m scitq2.yaml_runner --offline --yaml-module-path /tmp/tree template.yaml` resolves against `/tmp/tree/<path>.yaml`; without `--offline`, the runner errors rather than falling through.
- Deprecation: a template using `module: X.yaml` emits one deprecation warning per run and resolves the library path `X`.
- `{RESOURCE_ROOT}`: template referencing `{RESOURCE_ROOT}` in any field with no way to resolve it is rejected before workflow creation; template that does not reference it runs unchanged.
- Auto-upgrade exclude: `autoupgrade_exclude: ["metagenomics/*"]` prevents those modules from being seeded on subsequent startups; already-imported rows are left in place.
- Pin: an unpinned template run records resolved versions into `template_run.module_pins`; re-running with the recorded pins replays the exact same module content even after a subsequent `module upgrade`.
