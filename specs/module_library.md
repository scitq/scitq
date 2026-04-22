# Unified Versioned Module Library

## Status

Proposed. No code yet.

## Problem

Today scitq has two parallel, incompatible stores for YAML modules:

| | Bundled modules | Private modules |
|---|---|---|
| Location | `python/src/scitq2_modules/yaml/**/*.yaml` inside the `scitq2` Python package | `{script_root}/modules/*.yaml` on the server filesystem |
| Namespace | Path-based (`genetic/fastp`) | Flat, no slashes allowed (`filepath.Base(name) == name`) |
| Lifecycle | Shipped with the client release | Uploaded via `scitq module upload` |
| Versioning | Coupled to scitq package version | None — upload overwrites with `--force` |
| Resolved by | `_load_public_import` → reads from Python package on the worker running the runner | `_load_private_module` → searches pipeline dir + `{script_root}/modules` on the server |
| Updating | Wait for a scitq release → `pip install -U scitq2` everywhere | `scitq module upload --force` |

Concrete pains we hit:
- **A bundled-module bug fix needs a scitq release.** Example (recent): a `gzip -t` sanity check in `genetic/fastp.yaml` to catch truncated inputs. Today that change ships only when the whole package ships, on every worker's venv.
- **No site-specific overrides.** A lab wanting a different `fastp` adapter sequence must either fork the upstream module (losing upgradeability) or copy-paste into every template.
- **Private modules can't mirror the bundled taxonomy.** A team's internal aligner can't live at `genetic/my_aligner` next to `genetic/fastp`; it must be flat.
- **No history.** Overwriting a module silently changes every future template run that imports it. No way to pin a template to a specific module revision, and no audit trail.

## Goals

1. **One store, two origins.** Server-side module store holds both bundled and user-uploaded modules. `import: genetic/fastp` resolves the same way for both.
2. **Versioning.** Every module has an explicit `(path, version)` key. Existing templates keep working with whatever version was current at upload time.
3. **Origin provenance.** Each stored module knows whether it was seeded from the scitq package (`bundled`), uploaded by a user (`local`), or modified from bundled (`forked`).
4. **Path namespacing for private modules.** `scitq module upload --path genetic/my_aligner.yaml` works and is resolvable as `import: genetic/my_aligner`.
5. **Admin-controlled updates.** Admin decides when to pull newer bundled modules; forks are never clobbered silently.
6. **Offline / dev compatibility.** The `scitq2_modules` Python package remains usable as a fallback for local dry-runs without a server.

## Non-goals

- Three-way merge UIs for reconciling forks against upstream. `diff` + manual edit is sufficient.
- Curated category enforcement. No required `genetic/`, no allowed-prefix list. Folders are organisational, not mandatory.
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

- `path` — forward-slash path in a virtual namespace, no leading slash, no `..`, no empty segments. Examples: `genetic/fastp`, `genetic/bowtie2_host_removal`, `internal/my_aligner`, `hello`.
- `version` — tolerant semver: a leading `MAJOR.MINOR.PATCH` followed by an optional `-<suffix>`. Examples: `1.2.3`, `1.1.1-site`, `2.0.0-beta1`. Ordering: numeric MAJOR.MINOR.PATCH first, then suffix lexicographic, then a row without suffix sorts higher than one with (so `1.1.0` > `1.1.0-rc1`). New uploads MUST bump at least PATCH.

A "resolvable name" in a template is `path[@version]`:

```yaml
steps:
  - import: genetic/fastp                 # latest available
  - import: genetic/fastp@latest          # same — explicit form
  - import: genetic/fastp@1.2.0           # pinned to a concrete version
```

`@latest` is a magic value meaning "highest-ordered version present in the store at resolution time". Bare `path` (no `@`) is shorthand for `@latest`. This mirrors `workflow_template`'s use of `latest`. `latest` is never stored — the runner always records the resolved concrete version into `template_run.module_pins`, so a re-run with those pins replays exactly what ran the first time even if a newer version has since been uploaded.

`latest` is also a **reserved version string**: uploads with `--version latest` are rejected (would be ambiguous).

### Origin

Every module row has an `origin`:

- `bundled` — seeded from the scitq package, never modified locally. Content hash matches the package's shipped hash.
- `forked` — originated from `bundled` but diverged locally (admin edited `genetic/fastp` to add a site-specific adapter). Never overwritten by sync without `--force`.
- `local` — uploaded by a user, no bundled counterpart.

Origin is per-row (per version). Bumping a forked module to a new version resets the decision point: the next sync of bundled can either upsert a new bundled version alongside the fork (both live) or be blocked until admin resolves — see [Sync semantics](#sync-semantics).

### Resolution

When a template uses `import: <path>[@<version>]`:

1. Server looks up `(path, version)` in the `module` table. If `version` is omitted, the highest-version row wins.
2. If not found **and the runner is executing in client-offline mode** (no server access, e.g. local dry-run), fall back to the Python package at `scitq2_modules/yaml/<path>.yaml`.
3. Otherwise error: `module '<path>'[@<version>]' not found`.

Key property: **the server store always wins over the package fallback when the server is reachable.** This is what lets admin-uploaded patches take effect without a client release. The Python package is only a fallback for offline dev.

Private and bundled imports share one keyword (`import:`). The legacy `module:` keyword remains accepted for backward compatibility and is identical to `import:` under the new model.

## Server-side storage

### Schema

New table (not renaming the existing private-module filesystem layout; that's migrated, see below):

```sql
CREATE TABLE module (
    module_id    SERIAL PRIMARY KEY,
    path         TEXT NOT NULL,            -- e.g. 'genetic/fastp' (no leading slash, no extension)
    version      TEXT NOT NULL,            -- tolerant semver, see Identity
    content      BYTEA NOT NULL,           -- YAML bytes as-uploaded
    content_sha  TEXT NOT NULL,            -- sha256 hex of content, for sync detection
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
- `content` is stored in the DB, not the filesystem. Removes the `script_root/modules` quirk and makes modules backup/replicate with the rest of the DB.
- `content_sha` lets the sync job detect "has the bundled version on disk changed since I last seeded it".
- `bundled_sha` on a `forked` row records the upstream content it diverged from — powers the "your fork is N versions behind bundled" diagnostic.
- No soft-delete. Modules accumulate; templates pin what they used. Old versions can be pruned via an explicit `scitq module prune <path> --keep 5` if storage becomes a concern (out of scope for v1).

### Seeding bundled modules

Bundled content lives in the installed `scitq2_modules` Python package at `scitq2_modules/yaml/<path>.yaml`, each file carrying a `version:` field (required going forward on bundled modules).

Triggered by `scitq module upgrade` (admin only) and on server first-start:

```
for each bundled YAML file discovered under scitq2_modules/yaml/:
    path    = relative path with .yaml stripped (e.g. 'genetic/fastp')
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

### Forking semantics

A `local` upload at a path that already has a `bundled` row at a different version: just a `local` row. No special handling.

A `local` upload at a path that already has a `bundled` row **at the same version**: rejected. You can't collide with bundled for the same `(path, version)`. Bump the version.

Admin edits a bundled module via the CLI/MCP edit path (see below):
- The row's `origin` flips to `forked`, its `bundled_sha` is set to the pre-edit content hash, and the content updates in place (not a new version — this is the "I'm patching my deployment of this exact version" case).
- An explicit fork-as-new-version path is also available: `scitq module fork genetic/fastp@1.1.0 --new-version 1.1.1-site` creates a new `forked` row at `(genetic/fastp, 1.1.1-site)` without touching the original.

### Sync semantics

`scitq module upgrade` (admin-only) walks the installed `scitq2_modules/yaml/` tree and applies the seeding logic above. Dry-run by default, `--apply` to commit. Output:

```
$ scitq module upgrade
genetic/fastp                     1.2.0   bundled (new)         would insert
genetic/fastp                     1.1.0   forked                keep (local edits detected)
genetic/bowtie2_host_removal      1.0.3   bundled (up-to-date)  no-op
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
scitq module upload --path ./modules/my_aligner.yaml --as genetic/my_aligner

# Explicit version; if omitted, read from YAML 'version:'.
scitq module upload --path ./genetic/fastp.yaml --version 1.2.0 --as genetic/fastp

# Download. --version defaults to latest available.
scitq module download --name genetic/fastp
scitq module download --name genetic/fastp --version 1.1.0 -o fastp-1.1.0.yaml

# List. Flat by default, --tree groups by folder, --versions shows all versions per path.
scitq module list
scitq module list --tree
scitq module list --versions genetic/fastp

# Show provenance: origin, hash, who uploaded.
scitq module origin genetic/fastp
scitq module origin genetic/fastp@1.1.0

# Create a local fork of a bundled module at a new version.
scitq module fork genetic/fastp@1.1.0 --new-version 1.1.1-site

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
1. If server is reachable (SCITQ_SERVER is set and reachable):
     rpc ModuleGet(path=import_name, version=version) → content
2. Else (offline, local dry-run, server down):
     look up scitq2_modules/yaml/<import_name>.yaml in the installed package
     (version ignored; there is no versioning in the fallback)
3. Raise if neither finds anything.
```

Since YAML templates always execute server-side (the CLI ships YAML bytes to the server; the server's `yaml_runner` subprocess resolves the imports), branch 1 is the only one that runs in production. The package fallback (branch 2) keeps `yaml_runner.py` usable as a standalone tool for local dry-runs or development without a server — it just can't see server-side overrides in that mode.

### Pin recording

When a template runs through the YAML runner, for each `import: <path>` without an explicit version, the runner records the `(path, version)` actually resolved and writes it into `template_run.module_pins` (JSONB array). Re-running a stored `template_run` replays with the exact same module content even after a later `module upgrade`.

## Template pinning

Templates do not need to pin versions by default. Two escape hatches for when pinning matters:

- **Explicit pin in YAML**: `import: genetic/fastp@1.1.0`. The runner respects it.
- **Recorded resolution** (above): a template_run snapshots the versions it actually used. A "reproduce this run" flow can replay with the same pins even if the template YAML is unversioned.

New templates that care about reproducibility are expected to pin manually. scitq does not force pinning.

## Migration

One migration on the server:

1. `CREATE TABLE module (...)` as above.
2. For each file in the existing on-disk `{script_root}/modules/*.yaml`:
   - `path` = filename with `.yaml` stripped
   - `version` = value of the YAML `version:` field; if missing, `0.0.0`
   - `origin` = `L` (local)
   - `content_sha` = sha256(content)
   - Insert.
3. Immediately run `sync-modules --apply` to seed all bundled modules from the installed scitq2_modules package at their declared versions.
4. Leave the on-disk `{script_root}/modules` directory alone. An out-of-band cleanup step (next release) removes it once we're sure nothing reads from it.

For clients: updating `_load_module` to hit the server is the only runner change. Clients predating this spec continue to find bundled modules in the Python package (pre-migration behaviour), just without server-side override.

## Future improvements

Out of scope for v1, but worth noting so the design doesn't paint us into a corner:

- **Auto-prune of unreferenced bundled rows.** Over many `module upgrade` cycles the store accumulates old bundled versions. A retention policy ("drop `origin=B` rows older than N months with no `template_run.module_pins` reference, and no newer version in a fork chain that depends on them") keeps size bounded without breaking reproducibility. Deserves its own spec (`specs/module-retention.md`) covering reference-counting, grace periods, and admin overrides.
- **Server-only Python-DSL templates.** A flag on `workflow_template` that marks a template as non-runnable client-side would unblock layering Python modules on top of this store in a follow-up spec — the "client executes, server-store has no hook" problem disappears when the client is never the executor. Not needed for this spec; just flagging that the schema leaves room by keeping identity at `(path, version)` without a `kind` column (adding one later is a simple ALTER).

## Test plan

- Unit: seeding skips matching bundled rows, inserts new ones, preserves forks. Colliding content at same `(path, version)` refused without `--force`.
- Unit: upload with slashes accepted; upload with `..` or leading `/` rejected.
- Unit: version ordering handles `1.2.0` > `1.1.1-site` > `1.1.1-rc1` > `1.1.0` correctly.
- Integration: upload private `genetic/my_aligner`, import from a template, verify it loads. Upload a `local` at a path bundled already has: both visible, version-specific imports resolve the right one.
- Integration: admin edits bundled `genetic/fastp` via CLI; row flips to `forked`; `module upgrade` dry-run reports it as fork-preserved; `--apply` does not overwrite.
- Integration: pre-migration server with on-disk private modules is upgraded; all existing private modules appear in the new table with `origin=L`; templates that imported them still resolve.
- Offline: with `SCITQ_SERVER` unset, `_load_module genetic/fastp` falls back to the package copy.
- Pin: an unpinned template run records resolved versions into `template_run.module_pins`; re-running with the recorded pins replays the exact same module content even after a subsequent `module upgrade`.
