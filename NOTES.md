# A rewrite of scitq in Go

scitq v1, python version, has a lot of quality but suffers from several issues:
- the overall design is kind of fat with a few adhoc internal repetition like the fact that internal server tasks are completely unrelated to the user tasks,
- Some things are slugish: 
  - ansible for a start, and overall ansible brings little benefits in scitq context, each provider's library is so specific that a lot of things has to be redesigned, ansible dependancies are hellish, and I won't say again how slow ansible is but I'm crying blood each time I launch it...
  - python dependancies make deploy long and complex, copying a single binary would be so nice...
  - REST is uselessly verbose and heavy when things turn bad.

Also, as I am gaining more experience in Rust, I see that for less clever and more practical code it can be a serious handicap, and I am willing to let go a chance...

# source

## preparing things

### certificates

```sh
openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.pem -days 3650 -nodes -subj "/CN=localhost"
# optionally check with
openssl x509 -in server.pem -text -noout



### do it once
This is done once, so I do not need to redo it: 

```sh
brew install go golang-migrate protoc-gen-doc
go mod init  github.com/scitq/scitq
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
migrate create -ext sql -dir migrations -seq init_schema

```

In Postgres:
```sql
CREATE DATABASE scitq2;
CREATE USER scitq_user WITH PASSWORD 'dsofposiudipopipII9';
GRANT ALL PRIVILEGES ON DATABASE scitq2 TO scitq_user;
ALTER DATABASE scitq2 OWNER TO scitq_user;
```

### once in a while

This command should show if any lib may be updated:
```sh
go list -u -m all
go get -u all
```

### do it each time protocol change

There is now a global command to update stubs for the go/UI(svelte)/python part:

```sh
make proto-all
```

```sh
go mod tidy
protoc --go_out=. --go-grpc_out=. --proto_path=proto proto/taskqueue.proto
go run server/main.go
```

# basic usage

## quick run

```sh
# run the server
go run client/main.go 

# run the client
go run client/main.go --server localhost:50051 --concurrency 2 -name myworker
# (or just go run client/main.go --concurrency 2)

go run cli/main.go task create --container alpine --command "echo yes"
# in loop, used the build version
for i in $(seq 1 1000); do ./cli/bin/scitq-cli  task create --container alpine --command "echo yes"; done
go run cli/main.go task list --status P
go run cli/main.go task output --id 1

```

## compile for deploy

```sh
CGO_ENABLED=0 go build -o client/bin/scitq-client client/main.go
CGO_ENABLED=0 go build -o server/bin/scitq-server client/server.go
CGO_ENABLED=0 go build -o cli/bin/scitq-cli cli/main.go
```

## install on linux

```sh
apt install make postgreql
wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
echo !! >> /etc/profile.d/ZZZ-locale-functions.sh
cd scitq2
make install
sudo -u postgres createuser scitq_user -d -P
psql -h localhost -U scitq_user template1
# CREATE DATABASE scitq2;

cat <<EOF >/etc/systemd/system/scitq.service
[Unit]
Description=scitq
After=multi-user.target

[Service]
Type=simple
Restart=on-failure
Environment=HOME=/root
ExecStart=/usr/local/bin/scitq-server -config /etc/scitq.yaml

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable scitq
ssh-keygen
ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa
systemctl start scitq

```

# life cycle

## listing and creating

Server run with:
```sh
scitq-server -config sample_files/gmts.yaml
```

```sh
scitq-cli flavor list --filter "cost>0:eviction<=5"
scitq-cli worker deploy --flavor Standard_D2s_v4 --provider azure.primary 
```