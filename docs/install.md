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
