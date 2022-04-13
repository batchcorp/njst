VERSION ?= $(shell git rev-parse --short HEAD)
SERVICE = njst

GO = CGO_ENABLED=$(CGO_ENABLED) GOFLAGS=-mod=vendor go
CGO_ENABLED ?= 0
GO_BUILD_FLAGS = -ldflags "-X main.version=${VERSION}"

# Utility functions
check_defined = \
	$(strip $(foreach 1,$1, \
		$(call __check_defined,$1,$(strip $(value 2)))))
__check_defined = $(if $(value $1),, \
	$(error undefined '$1' variable: $2))

# Pattern #1 example: "example : description = Description for example target"
# Pattern #2 example: "### Example separator text
help: HELP_SCRIPT = \
	if (/^([a-zA-Z0-9-\.\/]+).*?: description\s*=\s*(.+)/) { \
		printf "\033[34m%-40s\033[0m %s\n", $$1, $$2 \
	} elsif(/^\#\#\#\s*(.+)/) { \
		printf "\033[33m>> %s\033[0m\n", $$1 \
	}

.PHONY: help
help:
	@perl -ne '$(HELP_SCRIPT)' $(MAKEFILE_LIST)

### Dev

.PHONY: setup/linux
setup/linux: description = Install dev tools for linux
setup/linux:
	GO111MODULE=off go get github.com/maxbrunsfeld/counterfeiter

.PHONY: setup/darwin
setup/darwin: description = Install dev tools for darwin
setup/darwin:
	GO111MODULE=off go get github.com/maxbrunsfeld/counterfeiter
	brew install doctl

.PHONY: run
run: description = Run $(SERVICE)
run:
	$(GO) run `ls -1 *.go | grep -v _test.go` -d

.PHONY: start/deps
start/deps: description = Start dependencies
start/deps:
	docker-compose up -d

### Build

.PHONY: build
build: description = Build $(SERVICE)
build: clean build/linux build/darwin

.PHONY: build/linux
build/linux: description = Build $(SERVICE) for linux
build/linux: clean
	GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -o ./build/$(SERVICE)-linux

.PHONY: build/darwin
build/darwin: description = Build $(SERVICE) for darwin
build/darwin: clean
	GOOS=darwin GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -o ./build/$(SERVICE)-darwin

.PHONY: clean
clean: description = Remove existing build artifacts
clean:
	$(RM) ./build/$(SERVICE)-*

### Test

.PHONY: test
test: description = Run Go unit tests
test: GOFLAGS=
test:
	$(GO) test ./...

.PHONY: testv
testv: description = Run Go unit tests (verbose)
testv: GOFLAGS=
testv:
	$(GO) test ./... -v

.PHONY: test/coverage
test/coverage: description = Run Go unit tests
test/coverage: GOFLAGS=
test/coverage:
	$(GO) test ./... -coverprofile c.out

### Docker

.PHONY: docker/build
docker/build: description = Build docker image
docker/build:
	docker build -t ghcr.io/batchcorp/$(SERVICE):$(VERSION) \
	-t ghcr.io/batchcorp/$(SERVICE):latest \
	-f ./Dockerfile .

.PHONY: docker/run
docker/run: description = Build and run container + deps via docker-compose
docker/run:
	docker-compose up -d

.PHONY: docker/push
docker/push: description = Push local docker image
docker/push:
	docker push ghcr.io/batchcorp/$(SERVICE):$(VERSION) && \
	docker push ghcr.io/batchcorp/$(SERVICE):latest

### Kube

.PHONY: kube/deploy/dev
kube/deploy/dev: description = Deploy image to kubernetes cluster
kube/deploy/dev:
	doctl kubernetes cluster kubeconfig save do-dev && \
	cat deploy.dev.yaml | \
	sed "s/{{VERSION}}/$(VERSION)/g" | \
	sed "s/{{SERVICE}}/$(SERVICE)/g" | \
	kubectl apply -f -

.PHONY: kube/deploy/prod
kube/deploy/prod: description = Deploy image to kubernetes cluster
kube/deploy/prod:
	aws eks --region us-west-2 update-kubeconfig --name batch-prod-1 && \
	cat deploy.prod.yaml | \
	sed "s/{{VERSION}}/$(VERSION)/g" | \
	sed "s/{{SERVICE}}/$(SERVICE)/g" | \
	python3 scripts/vaultelier.py | \
	kubectl apply -f -

.PHONY: kube/delete
kube/delete: description = Deletes pods from cluster
kube/delete:
	kubectl delete pods -l app=$(SERVICE)

.PHONY: kube/logs
kube/logs: description = Get pod logs
kube/logs:
	kubectl logs -l app=$(SERVICE)

.PHONY: kube/logsf
kube/logsf: description = Get pod logs
kube/logsf:
	kubectl logs -l app=$(SERVICE)
