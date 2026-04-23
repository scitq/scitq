# Install

## Prerequisites
There are three build requirements:
- Go : see [Go installation](https://go.dev/doc/install):
    - [download](https://go.dev/dl/) the archive, 
    - untar it with `rm -rf /usr/local/go && tar -C /usr/local -xzf go1.25.3.linux-amd64.tar.gz`,
    - add the path if needed with `export PATH=$PATH:/usr/local/go/bin`
- Node.js : not needed for production but needed to compile the Svelte UI, see [Node download](https://nodejs.org/en/download)
- Make : this should be available in your favorite distribution: `apt install make`
- Git : to download and manage the code: `apt install git`

What follows is required for production but not for build.
- PostgreSQL : this should be available in your favorite distribution: `apt install postgresql`
- Python 3.8+ : this should be available in your favorite distribution: `apt install python3`
- Optional (but recommanded) : Docker : `apt install docker.io`

## Installation

- Download the code: `git clone https://github.com/scitq/scitq`
- Compile and install: `cd scitq && sudo make install`

## What you get

Build will give you three binaries installed in `/usr/local/bin/`:
- `scitq-server` : the server binary, including the Go engine, the Svelte UI and the YAML/Python template engine, see below for configuration details,
- `scitq-client` : the client binary, e.g. what runs on a worker, usually deployed automatically, but you can install it manually also, see below,
- `scitq` : the CLI binary, ready to use, see usage.

## Configuration

scitq uses a single YAML configuration file, typically `/etc/scitq.yaml`.

A complete example is provided here:

📄 [sample_files/scitq.yaml](https://github.com/scitq/scitq/blob/main/sample_files/scitq.yaml)

You can copy and adapt it for your environment.

Please find a detailed explanation about config items :
NB in what follow, you must set the *YAML key* as a yaml entry, setting `scitq.port` to 50051 means to write in YAML:

```yaml
scitq:
    port: 50051
```

See the example.

---

{{ include-markdown "../generated/config.md" }}
*(If not displayed, see [reference/configuration.md](reference/configuration.md).)*

## Setup the server service

You'll need to setup the database like this:

```sh
# Create the database
sudo -u postgres createdb scitq

# Create a user and set a password (you’ll be prompted)
sudo -u postgres createuser -P scitq_user

# Make the new user the owner of the new DB
sudo -u postgres psql -c "ALTER DATABASE scitq OWNER TO scitq_user;"
```

Then create and activate the service by:

```sh
# Create the service
sudo curl -L https://raw.githubusercontent.com/scitq/scitq/main/sample_files/scitq.service \
  -o /etc/systemd/system/scitq.service

# Reload systemd configuration
sudo systemctl daemon-reload

# Enable and start the service
sudo systemctl enable --now scitq.service
```

## Installing manually a worker

Most of the time worker are deployed automatically using a provider. It is however possible to deploy manually a worker:

- copy the `scitq-client` binary to the worker,
- install docker on the worker : `apt install docker.io`
- if you have private docker registry, copy `.docker/config.json` to worker `/root/.docker/config.json`

### Prevent unattended systemd upgrades

On **manually installed scitq worker nodes**, it is recommanded to prevent unattended upgrades from touching `systemd` (it can trigger `systemd daemon-reexec` and stop running workers). Create `/etc/apt/preferences.d/systemd` with:

```
Package: systemd systemd-sysv libsystemd0 libpam-systemd
Pin: release o=Ubuntu
Pin-Priority: 1
```

This blocks unattended-upgrades and `apt-daily` from upgrading `systemd`, while still allowing manual upgrades.

This is not useful on automatically deployed workers where unattended upgrades are disabled entirely at install time.

Launch it with :
```sh
scitq-client --store /scratch --token MySecretToken  -permanent
```
Where MySecretToken match server YAML `scitq.worker_token`.

You can set it up as a service by creating `/etc/systemd/system/scitq-client.service`:

```ini
[Unit]
Description=scitq-client
After=multi-user.target

[Service]
Type=simple
Restart=on-failure
Environment=HOME=/root
ExecStart=/usr/local/bin/scitq-client --store /scratch --token MySecretToken  -permanent

[Install]
WantedBy=multi-user.target
```

And then activate it as usual:
```sh
# Reload systemd configuration
sudo systemctl daemon-reload

# Enable and start the service
sudo systemctl enable --now scitq-client.service
```

# Production polishing

## Using proper SSL certificates (Let's Encrypt)

By default, scitq uses self-signed certificates, which is quite inconvenient with modern browsers, so it is recommended to use real world certificates and Let's Encrypt free ones are perfect for this.

- Install certbot for `other` types of webserver and `linux-pip` : https://certbot.eff.org/instructions?ws=other&os=pip
- You cannot specify directly letsencrypt files in `scitq.yaml` because of some permission issues, so you should have something like:

```yaml
scitq:
  [...]
  certificate_key: "/etc/scitq/privkey.pem"
  certificate_pem: "/etc/scitq/fullchain.pem"
```

- create the folder: `mkdir /etc/scitq`
- then create a certbot post-hook:
```sh
SERVER=my.server.fqdn
cat <<EOF >/usr/local/bin/copycerts.sh
#!/bin/sh
cp /etc/letsencrypt/live/$SERVER/fullchain.pem /etc/scitq/
cp /etc/letsencrypt/live/$SERVER/privkey.pem /etc/scitq/
EOF
chmod a+x /usr/local/bin/copycerts.sh
```
- then in the crontab (by default in `/etc/crontab`) entry certbot made you add, change this:
```sh
0 0,12 * * * root sleep XXXX && certbot renew -q
```
to this:
```sh
0 0,12 * * * root sleep XXXX && certbot renew --post-hook /usr/local/bin/copycerts.sh -q
```

## Backuping

To backup a scitq install, backup:
- PostgreSQL (using pg_dump) : it contains all history
- `/etc/scitq.yaml` : your configuration (it contains passwords for your providers so be careful)
- `/var/lib/scitq` : it contains your templates, modules, task logs and the Python environment (this is the default location, the location may be changed in `/etc/scitq.yaml`)

## Upgrading

Most upgrades are stateless — swap the server and CLI binaries, restart
the service, done. A few releases carry schema migrations or library
changes that need one or two follow-up commands. Apply them once, after
every service has started cleanly on the new binary.

### Routine upgrade checklist

1. Stop the service: `systemctl stop scitq`.
2. Install the new binaries (`scitq-server`, `scitq`, and the
   `scitq-client` that workers pull).
3. Start the service: `systemctl start scitq`. Schema migrations under
   `server/migrations/` apply automatically at startup; check
   `journalctl -u scitq -n 200` for any migration warnings.
4. Rebuild the UI if you serve it locally (`make ui-build`).
5. Verify: `scitq --version`, `scitq workflow list`, and a one-off
   `scitq task list --limit 1` to confirm the server responds.

### Upgrading from a pre-library release (YAML module library)

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

and re-login (`scitq login --user <your-user> --password …`) — the
`is_admin` bit is cached at session creation, not re-read per request.

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

### Troubleshooting

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
