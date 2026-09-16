.PHONY: test vet fmt-check simulate check mutation mutation-self-test

test:
	go test -race -count=1 -cover ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; exit 1)

simulate:
	go run ./cmd/hoarder-sim

check: fmt-check vet test mutation-self-test

mutation-self-test:
	python3 -m unittest discover -s scripts -p '*_test.py'

mutation: mutation-self-test
	python3 scripts/mutation_check.py
