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
- `scitq-server` : the server binary, including the go engine, the Svelte UI and the python DSL, see below for configuration details,
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
- `/var/lib/scitq` : it contains your scripts, task logs and python DSL environment (this is the default location, the location may be changed in `/etc/scitq.yaml`)
