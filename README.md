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
brew install go
go mod init  github.com/gmtsciencedev/scitq2
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`
```

### once in a while

This command should show if any lib may be updated:
```sh
go list -u -m all
go get -u all
```

### do it each time protocol change
```sh
go mod tidy
protoc --go_out=. --go-grpc_out=. --proto_path=proto proto/taskqueue.proto
go run server/main.go
```