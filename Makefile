.PHONY: build test test-unit test-integration vet lint fmt check cover clean

build:
	go build ./...

test:
	go test ./... -race -count=1

test-unit:
	go test $(shell go list ./... | grep -v /tests/) -race -count=1

test-integration:
	go test ./tests/... -race -count=1

vet:
	go vet ./...

fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Files not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint: vet fmt
	golangci-lint run

cover:
	go test ./... -race -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1
	@echo "To view HTML report: go tool cover -html=coverage.out"

check: build lint test

clean:
	go clean ./...
	rm -f coverage.out
