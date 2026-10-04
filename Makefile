GOPATH := $(shell go env GOPATH | tr '\\' '/')
GOEXE := $(shell go env GOEXE)
GOHOSTOS := $(shell go env GOHOSTOS)
GOHOSTARCH := $(shell go env GOHOSTARCH)
GOHOSTARCHVERSION := $(shell go env "GO$(shell go env GOHOSTARCH | tr '[:lower:]' '[:upper:]')")
GORELEASER := $(GOPATH)/bin/goreleaser
SOURCE_FILES := *.go

# see https://github.com/goreleaser/goreleaser
GORELEASER_VERSION := 2.18.2

all: clean build

init: $(GORELEASER) $(GOVERSIONINFO)
	go mod download

$(GORELEASER):
	go install github.com/goreleaser/goreleaser/v2@v$(GORELEASER_VERSION)

build: init $(SOURCE_FILES)
	GOAMD64=v3 \
		$(GORELEASER) build --snapshot --clean --skip=validate --single-target

release-snapshot: init $(SOURCE_FILES)
	$(GORELEASER) release --snapshot --clean --skip=publish

release: init $(SOURCE_FILES)
ifeq ($(CI),true)
	$(GORELEASER) release --clean
else
	$(GORELEASER) release --clean --skip=publish
endif

clean:
	rm -rf dist tmp* *.log *.exe

.PHONY: all init build release release-snapshot clean
