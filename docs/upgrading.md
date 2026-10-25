# Upgrading

Upgrade the server first, then the CLIs and the workers. Most upgrades need
nothing else: the server applies its database migrations itself when it
starts. A release that needs more says so below, and in the
[Changelog](changelog.md).

## Routine upgrade

Upgrade the server first, then the workers. Which versions can run together
during the upgrade is described in [Compatibility and versioning](reference/compatibility.md#mixing-versions).

1. Back up the database (`pg_dump`). Migrations only go forward, so this
   backup is how you return to the previous release if needed.
2. Install the new binaries (`scitq-server`, `scitq`, and the
   `scitq-client` that workers pull), then restart the server on them.
   From a source checkout, `make server-upgrade` does both: it installs
   the binaries, then sends SIGUSR1 to the server, which stops accepting
   new admin jobs (worker creation and deletion), lets the running ones
   finish, and exits; systemd restarts it on the new binary. Otherwise,
   install the binaries and run `systemctl restart scitq`.

   Install the binaries before sending SIGUSR1 yourself: systemd restarts
   the server immediately, on whatever binary is in place at that moment.
3. Check the startup. Schema migrations under `server/migrations/` apply
   automatically; `journalctl -u scitq -n 200` shows any migration
   warnings.
4. Rebuild the UI if you serve it locally (`make ui-build`).
5. Verify: `scitq --version`, `scitq workflow list`, and a one-off
   `scitq task list --limit 1` to confirm the server responds.
6. Upgrade the workers: `scitq worker list --needs-upgrade` shows those on
   an older build; `scitq worker upgrade --all` upgrades them, each one
   when idle.

## Going back

Downgrading is a restore, not a reinstall. Migrations only go forward: once a
newer server has started, its database schema no longer matches the older
binary, and reinstalling that binary alone leaves a server that fails at
startup or misreads its data. To go back, stop the server, restore the
database backup taken before the upgrade, then reinstall the previous
binaries.

## To 1.0

Follow the routine upgrade, then check these points. Each is a change of
behaviour you may rely on today.

- **The worker token is for workers only.** It now allows only the calls
  workers make. A script that uses `scitq.worker_token` as its credential
  for anything else — listing workflows, submitting tasks, deploying
  workers — is refused with `PermissionDenied`: give it a user account
  (`scitq user create`) and log in with it.
- **Upgrade every CLI.** Reading task logs (`scitq task stdout|stderr|logs`)
  now requires a session token on the log stream, which CLIs before 1.0 do
  not send. Older workers keep working until you upgrade them: the server
  accepts their logs for their own tasks.
- **Sessions expire after 30 days without use**, and in any case a year
  after login. A long-lived `SCITQ_TOKEN` in a script or an MCP client
  configuration needs a fresh `scitq login` once it has been idle for 30
  days. A changed password ends your other sessions.
- **MCP clients need a session from `scitq login`.** A token the server has
  no session for (one you logged out, for instance) is refused instead of
  silently re-registered.
- **`scitq_auth` tasks get a token of their own**, which allows
  `scitq file copy` and `scitq file list` only, while the task runs. A task
  that called other `scitq` commands with the injected token needs another
  credential. Tasks started by workers before 1.0 keep the worker token
  until those workers are upgraded.
- **All recruiters of a step share one `rounds` value.** Creating a
  recruiter whose `rounds` differs from the step's other recruiters is
  refused; updating `rounds` changes it on all of them. Existing recruiters
  that disagree keep working until you update them.
- **`oom` means out of memory.** A task killed with exit 137 is classified
  `oom` only when Docker reports `OOMKilled`; other kills (a Docker daemon
  restart, `docker kill`) are now `other`. Queries or alerts on
  `failure_class = 'oom'` count fewer tasks.
- **The event socket (`/ws`) needs a session**, and `/WorkerToken` is gone.
  Rebuild the UI from the same release (`make ui-build`): the UI from 1.0
  authenticates its socket and no longer uses that endpoint.

## From a pre-library release (YAML module library)

Releases that shipped the versioned YAML module library — bundled,
local, and forked modules in a single server-side store — need an
explicit admin-initiated step to seed the bundled rows. The rest is
automatic.

**1. Automatic at server startup.** The first start of the new server
reads `{script_root}/modules/*.yaml` (default `/scripts/modules/`) and
inserts each file into the `module` table as `origin=local` at path
derived from the filename. Version comes from the YAML's `version:`
field, or `0.0.0` if absent. The migration is idempotent — safe to
leave the legacy directory in place, subsequent restarts are no-ops.

Audit what landed:

```sh
scitq module list --origin local
```

Entries addressed at top-level paths (no namespace prefix, e.g. just
`megahit`, `my_aligner`) are the ones your previous `module:`
references resolved against. They keep working exactly as before.

**2. Admin-initiated: seed the bundled modules.** Bundled YAML modules
(shipped with the `scitq2_modules` Python package — `genomics/fastp`,
`metagenomics/meteor2`, etc.) are **not** auto-imported. Seed them with:

```sh
# Dry-run — shows what would change
scitq module upgrade

# Commit
scitq module upgrade --apply
```

`module upgrade` requires admin privileges. If the logged-in user is
not flagged admin, the command returns
`admin privileges required to upgrade bundled modules`. Grant the flag
in the database:

```sh
sudo -u postgres psql scitq2 -c \
  "UPDATE scitq_user SET is_admin=true WHERE username='<your-user>';"
```

— it takes effect on that user's next call; no re-login is needed.

**3. Verify the shape of the library.**

```sh
scitq module list --tree                 # grouped by namespace
scitq module list --origin bundled       # the newly-seeded rows
scitq module origin genomics/fastp       # provenance of a specific path
```

**4. Update your templates** if they use `import:` (public) paths that
referenced the old `genetic/` namespace. Releases that introduced the
library reorganised bundled modules under `genomics/` and
`metagenomics/`; the `genetic/` namespace is not shipped. Replace in
each template:

| Before | After |
|---|---|
| `import: genetic/fastp` | `import: genomics/fastp` |
| `import: genetic/multiqc` | `import: genomics/multiqc` |
| `import: genetic/seqtk_sample` | `import: genomics/seqtk_sample` |
| `import: genetic/bowtie2_host_removal` | `import: metagenomics/bowtie2_host_removal` |
| `import: genetic/eskrim` | `import: metagenomics/eskrim` |
| `import: genetic/megahit` | `import: metagenomics/megahit` |
| `import: genetic/metaphlan` | `import: metagenomics/metaphlan` |
| `import: genetic/meteor2` | `import: metagenomics/meteor2` |
| `import: genetic/meteor2_catalog` | `import: metagenomics/meteor2_catalog` |

Bump the template's `version:` and re-upload. Private modules
(`module: X.yaml`, resolved against `{script_root}/modules/`) are
unaffected.

**5. Optional cleanup.** Once no template or active `template_run`
references a pre-library path, stale `origin=local` rows auto-imported
in step 1 can be deleted — but there is no operational reason to
hurry. They cost near-zero and keep replayability of older workflows.

### Troubleshooting the library upgrade

- **`Public module not found: modules/X`** on template run — the
  template uses `import: modules/X` (library lookup), but the file is
  private at `{script_root}/modules/X.yaml`. Change to
  `module: X.yaml` (private loader).
- **`admin privileges required`** on `module upgrade` / `module fork`
  / `user create` — see the admin-grant snippet above.
- **`scitq module list --origin local` rejected as unknown argument** —
  you are on a pre-`--origin`-filter CLI. Redeploy the `scitq` binary.
- **`duplicate key value violates unique constraint` during
  `module upgrade --apply`** — a local upload has collided with a
  bundled version. Review with `scitq module list --versions <path>`;
  bump or remove the offending local row.
