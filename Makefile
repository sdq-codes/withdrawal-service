.PHONY: run migrate reconcile test race vet build docker clean

GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/sdq-codes/withdrawal-service/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/sdq-codes/withdrawal-service/version.BuildTime=$(BUILD_TIME)

run:
	go run . serve

migrate:
	go run . migrate

reconcile:
	go run . reconcile

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/withdrawal-service .

docker:
	docker build --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t withdrawal-service .

clean:
	rm -rf bin
