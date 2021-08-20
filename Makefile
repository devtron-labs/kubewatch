.PHONY: default build docker-image test stop clean-images clean

BINARY = kubewatch

VERSION=
BUILD=

PKG            = github.com/devtron-labs/kubewatch
TRAVIS_COMMIT ?= `git describe --tags`
GOCMD          = go
BUILD_DATE     = `date +%FT%T%z`
GOFLAGS       ?= $(GOFLAGS:)
LDFLAGS       := "-X '$(PKG)/cmd.gitCommit=$(TRAVIS_COMMIT)' \
		          -X '$(PKG)/cmd.buildDate=$(BUILD_DATE)'"

default: build test

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$(GOCMD)" build ${GOFLAGS} -ldflags ${LDFLAGS} -o "${BINARY}"

docker-image:
	@docker build -t "${BINARY}" .

test:
	"$(GOCMD)" test -race -v $(shell go list ./... | grep -v '/vendor/')

stop:
	@docker stop "${BINARY}"

clean-images: stop
	@docker rmi "${BUILDER}" "${BINARY}"

clean:
	"$(GOCMD)" clean -i
