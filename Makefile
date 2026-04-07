.PHONY: build test test-unit test-integration test-e2e vet lint fmt check cover bench clean

build:
	go build ./...

test:
	go test ./... -race -count=1

test-unit:
	go test $(shell go list ./... | grep -v /tests/) -race -count=1

test-integration:
	go test ./tests/integration/... -race -count=1

test-e2e:
	go test ./tests/e2e/... -race -count=1 -v

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

bench:
	go test -bench=. -benchmem -count=1 -run=^$$ ./...

check: build lint test

clean:
	go clean ./...
	rm -f coverage.out
