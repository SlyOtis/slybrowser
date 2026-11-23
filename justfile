set dotenv-load

APP_NAME := "slyb"

default:
    @just --list

build:
    go build -ldflags "-X github.com/slyotis/slybrowser/internal/slyb.Version=${SLYB_VERSION:-dev}" -o dist/{{APP_NAME}} ./cmd/slyb

install: install-cli
    `go env GOPATH`/bin/{{APP_NAME}} install

install-cli: build
    install -m 755 dist/{{APP_NAME}} `go env GOPATH`/bin/{{APP_NAME}}
    `go env GOPATH`/bin/{{APP_NAME}} version

install-cli-completion: install-cli
    `go env GOPATH`/bin/{{APP_NAME}} completion --auto

install-cli-skill: install-cli
    `go env GOPATH`/bin/{{APP_NAME}} skill install

test name="":
    go vet ./...
    go test ./... {{ if name != "" { "-run " + quote(name) } else { "" } }}

dev *args:
    go run ./cmd/slyb {{args}}

import source *args:
    go run ./cmd/import {{source}} {{args}}
