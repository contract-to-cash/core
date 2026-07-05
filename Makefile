.PHONY: build test test-all test-unit test-integration test-e2e test-lint-rules vet lint fmt check cover bench clean

build:
	go build ./...

# test runs the library module suite. tests/e2e/ is its own Go module (so runn
# and its heavy transitive deps stay out of the library go.mod) and is NOT
# matched by ./... here — run it separately via test-e2e / test-all.
test:
	go test ./... -race -count=1

# test-all runs the library suite and the separate e2e module.
test-all: test test-e2e

test-unit:
	go test $(shell go list ./... | grep -v /tests/) -race -count=1

test-integration:
	go test ./tests/integration/... -race -count=1

# test-e2e runs the separate tests/e2e module (depends on runn via its own
# go.mod with a replace back to the parent).
test-e2e:
	cd tests/e2e && go test ./... -race -count=1 -v

test-lint-rules:
	go test ./tests/lintcheck/... -count=1

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
