# A rewrite of scitq in Rust

scitq v1, python version, has a lot of quality but suffers from several issues:
- the overall design is kind of fat with a few adhoc internal repetition like the fact that internal server tasks are completely unrelated to the user tasks,
- Some things are slugish: 
  - ansible for a start, and overall ansible brings little benefits in scitq context, each provider's library is so specific that a lot of things has to be redesigned, ansible dependancies are hellish, and I won't say again how slow ansible is but I'm crying blood each time I launch it...
  - python dependancies make deploy long and complex, copying a single binary would be so nice...
  - REST is uselessly verbose and heavy when things turn bad.

Also, as I am gaining more experience in Rust, I appreciate more and more the solidity of Rust developments that is hard to achieve in Python.

# source

## preparing things

### preparing proto

- run `cargo build` in common and in the source files that use tonic, do `use common::task_proto;`

### preparting database

Decision is taken to use postgreSQL only

#### 

CREATE USER scitq2_user WITH PASSWORD 'your_secure_password';

CREATE DATABASE scitq2;

GRANT ALL PRIVILEGES ON DATABASE scitq2 TO scitq2_user;


These commands should be passed in `./server`:

- install sqlx cli : `cargo install sqlx-cli`
- create file `touch scitq.db`
- initialize the database `cargo sqlx migrate run --database-url=sqlite://./scitq.db`
- prepare database: `cargo sqlx prepare --database-url=sqlite://scitq.db`