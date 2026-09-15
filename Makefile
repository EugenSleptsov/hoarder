.PHONY: test vet fmt-check simulate check

test:
	go test -race -count=1 -cover ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; exit 1)

simulate:
	go run ./cmd/hoarder-sim

check: fmt-check vet test
