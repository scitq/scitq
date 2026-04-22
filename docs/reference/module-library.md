# Module library

scitq keeps every YAML module in a single **versioned server-side store**. Bundled modules (shipped with the `scitq2_modules` Python package) and user-uploaded modules live side by side in the same table, addressed by `(path, version)`. Admins patch bundled modules the same way users upload their own — no client release required.

This page is the reference. For day-to-day usage in workflow templates see [Modules in YAML templates](../usage/yaml-templates.md#modules).

---

## Concepts

### Identity

Each module row is identified by a pair:

- **`path`** — forward-slash namespace path, no leading slash, no extension. Example: `genetic/fastp`, `internal/my_aligner`, `hello`.
- **`version`** — tolerant semver. Leading `MAJOR.MINOR.PATCH` optionally followed by a `-<suffix>`. Examples: `1.2.3`, `1.1.0-rc1`, `1.0.0-site`. Ordering is numeric on `MAJOR.MINOR.PATCH`, then lexicographic on suffix, with suffix-less versions sorting higher (`1.1.0` > `1.1.0-rc1`). Every new upload must bump at least `PATCH`. `latest` is a reserved alias (see below) and cannot be used as a concrete version.

In templates you can omit the version (same as `@latest`) or pin it:

```yaml
steps:
  - import: genetic/fastp              # highest version in the library
  - import: genetic/fastp@latest       # same thing, explicit
  - import: genetic/fastp@1.0.0        # pinned
```

### Origin

Every row has an **origin** that records where it came from:

- `bundled` — seeded from the `scitq2_modules` Python package, unchanged. Content hash matches the package's shipped hash.
- `local` — uploaded by a user, no bundled counterpart.
- `forked` — started as `bundled` then diverged locally (admin edited a bundled module in place, or explicitly forked it to a new version). Never overwritten by `module upgrade` without `--force`.

### Companion modules (`requires:`)

A module can declare `requires:` listing other modules that must always accompany it in a workflow. Typical use is a compute module that requires a one-off setup module (e.g. catalog download). The yaml_runner:

1. Auto-injects any required module not already explicitly imported by the template, as a synthetic `- import: <path>` step ahead of the requiring step.
2. Extends the requiring step's `depends:` list with the required modules' step names.
3. Resolves `requires:` transitively — a required module's own `requires:` are pulled in the same way.

Template authors can leave the plumbing to the module author. For details and examples see [YAML templates — `requires:`](../usage/yaml-templates.md#requires-companion-modules-a-module-pulls-in).

### Resolution order

At template run time, `_load_module(path, version)` in `yaml_runner`:

1. If `SCITQ_SERVER` is set, RPC `DownloadModule(path[@version])` → use the library row.
2. Otherwise (offline / direct `python -m scitq2.yaml_runner` run), read `scitq2_modules/yaml/<path>.yaml` from the installed package.

The server is authoritative when reachable. The package fallback keeps local dry-runs working without a server.

### Pinning and reproducibility

When a template is executed server-side, every resolution is recorded in the JSONB column `template_run.module_pins`:

```json
[
  {"ref": "genetic/fastp", "path": "genetic/fastp", "version": "1.0.0", "source": "server"},
  {"ref": "internal/my_aligner@1.2.3", "path": "internal/my_aligner", "version": "1.2.3", "source": "server"}
]
```

`latest` is never stored — only the concrete version the runner actually loaded. Replay paths can read back the exact pin set and reproduce a run bit-for-bit even after a later `module upgrade` ships newer content.

---

## Admin workflows

### Seeding / refreshing bundled modules (`module upgrade`)

After a scitq release that changes any bundled YAML:

```sh
# Review what would change
scitq module upgrade

# Commit
scitq module upgrade --apply
```

`module upgrade` walks the installed `scitq2_modules/yaml/` tree (inside the server's venv), parses the `version:` field of each file, hashes the content, and diffs against the `module` table:

| Library state | Action | Report line |
|---|---|---|
| `(path, version)` absent | Insert as `origin=bundled` | `bundled (new)` |
| `origin=bundled`, same SHA | Skip | `bundled  up-to-date` |
| `origin=bundled`, different SHA at the **same** version | Refuse (packaging bug on our side) unless `--force` | `CONFLICT same (path,version) re-shipped with different bytes` |
| `origin=forked` at the same version | Skip — never clobber a fork | `forked   keep (local edits detected)` |
| `origin=local` at the same version | Refuse — local uploads must not collide with bundled versions | `CONFLICT local upload collides with a bundled version number` |

Bundled modules that ship a *new* version for an existing path just get a new row — the old `bundled` / `forked` / `local` rows at older versions stay untouched, visible via `module list --versions <path>`.

### Site-specific fork (`module fork`)

```sh
scitq module fork genetic/fastp@1.0.0 --new-version 1.0.0-site
scitq module download --name genetic/fastp@1.0.0-site -o /tmp/fastp-site.yaml
# … edit /tmp/fastp-site.yaml …
scitq module upload --path /tmp/fastp-site.yaml --as genetic/fastp --force
```

The fork starts as a copy of the source row with `origin=forked` and `bundled_sha = <source sha>`. Subsequent `--force` uploads keep it marked `forked`. `module origin` will flag a fork as *outdated* if a newer `bundled` row has shipped since the fork (`fork_is_outdated` field).

### Admin-less patching via in-place edit

If an admin just overwrites an existing `bundled` row with `upload --force`:

1. The row's `origin` flips to `forked`.
2. Its `bundled_sha` is populated with the pre-edit content hash.
3. Future `module upgrade` dry-runs report the row as `forked (keep)`.

This is the cheapest way to ship a hotfix without bumping versions — useful for `1.0.0-site` experiments before they're stable enough to tag.

### Uploading a user module

```sh
# filename → path. '--as' overrides the default path derivation.
scitq module upload --path modules/my_aligner.yaml
# server path = 'my_aligner'

scitq module upload --path modules/my_aligner.yaml --as internal/my_aligner
# server path = 'internal/my_aligner'

# Duplicate (path, version) is rejected unless --force
scitq module upload --path modules/my_aligner.yaml --force
```

The YAML content must carry a top-level `version:` field — uploads without it are rejected with a clear error. `version: latest` is rejected as well (reserved alias).

---

## CLI reference

| Command | Description |
|---|---|
| `scitq module upload --path X [--as P] [--force]` | Upload a YAML module. Path auto-derived from filename if `--as` is absent. `--force` overwrites an existing `(path, version)` in place. |
| `scitq module list` | Flat list of `path@version` rows with an origin marker (📚 bundled, 👤 local, 🍴 forked). |
| `scitq module list --tree` | Group rows by folder prefix. |
| `scitq module list --versions <path>` | Every version at a single path. |
| `scitq module list --latest` | Only the highest version per path. |
| `scitq module download --name <ref> [-o FILE]` | Fetch a module by `path`, `path@version`, or `path@latest`. Prints to stdout if `-o` is omitted. |
| `scitq module origin <ref>` | Print provenance: origin, content SHA, bundled SHA (on forks), uploader, description, and a flag if a fork is outdated. |
| `scitq module fork <ref> --new-version V` | **Admin**: clone a module row into a new `(path, V)` with `origin=forked`. |
| `scitq module upgrade [--apply]` | **Admin**: seed/update bundled rows from the installed `scitq2_modules` package. Dry-run by default. |

---

## MCP reference

For chat-based administration, the same functionality is exposed as MCP tools:

| Tool | Purpose |
|---|---|
| `upload_module` | Upload a module (accepts slashed paths and a version taken from the YAML content). |
| `download_module` | Fetch a module by `path` or `path@version`. |
| `list_modules` | Backward-compatible flat list. Structured `entries` field is also populated for newer clients. |
| `module_origin` | Provenance lookup. |
| `fork_module` | Admin: fork a module row to a new version. |
| `upgrade_modules` | Admin: seed/update bundled modules; `apply=true` to commit. |

---

## Database schema

```sql
CREATE TABLE module (
    module_id    SERIAL PRIMARY KEY,
    path         TEXT NOT NULL,
    version      TEXT NOT NULL,
    content      BYTEA NOT NULL,
    content_sha  TEXT NOT NULL,
    origin       CHAR(1) NOT NULL CHECK (origin IN ('B','L','F')),
    bundled_sha  TEXT,
    uploaded_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    uploaded_by  INTEGER REFERENCES scitq_user(user_id) ON DELETE SET NULL,
    description  TEXT,
    UNIQUE (path, version)
);

CREATE INDEX module_path_idx ON module (path);

ALTER TABLE template_run ADD COLUMN module_pins JSONB;
```

Origin is stored as `B`/`L`/`F` on disk and surfaced as `bundled`/`local`/`forked` at every public boundary (RPC responses, CLI output, MCP).

---

## Migration from pre-library servers

Servers upgraded from a pre-library scitq run a one-time idempotent migration at startup:

1. Every file in `{script_root}/modules/*.yaml` is inserted into the `module` table as `origin=local`. The `version:` field from the YAML is used; missing versions are imported as `0.0.0` so the row remains addressable.
2. The on-disk directory is left in place — it is no longer the authoritative source, but old installs that still read from it aren't affected.
3. Uploads and `module upgrade` are safe to run immediately after the migration completes.

The migration is idempotent: re-running the server does not duplicate rows.

---

## Backward compatibility

- **gRPC**: every pre-library RPC (`UploadModule`, `ListModules`, `DownloadModule`) keeps its request/response shape. New RPCs (`ListModulesFiltered`, `UpgradeBundledModules`, `GetModuleOrigin`, `ForkModule`) are additive. `ModuleList` has a new `entries` field; the old `modules` string list is unchanged.
- **CLI**: every pre-library invocation works identically. New flags (`--as`, `--tree`, `--versions`, `--latest`, `--apply`, `--new-version`) are all opt-in.
- **Templates**: the `module:` keyword is kept as a synonym for `import:`. Flat names (`my_aligner` without slashes) keep resolving to the root of the namespace.
- **Offline runs**: `python -m scitq2.yaml_runner <template.yaml>` without a reachable scitq server falls back to the installed `scitq2_modules` Python package, matching the pre-library behaviour exactly.

---

## Future improvements

Out of scope today; captured in [specs/module_library.md](https://github.com/scitq/scitq/blob/main/specs/module_library.md):

- Auto-prune policy for unreferenced `bundled` rows.
- Server-only Python-DSL template flag that would allow layering Python modules onto this store (today they stay in the pip-installed `scitq2_modules` package — see the spec's "Why YAML only" section for the rationale).
