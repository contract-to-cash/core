.PHONY: build test vet lint clean

build:
	go build ./...

test:
	go test ./... -race -count=1

vet:
	go vet ./...

lint: vet
	@which golangci-lint > /dev/null 2>&1 || echo "golangci-lint not installed, skipping"
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || true

clean:
	go clean ./...

check: build vet test
