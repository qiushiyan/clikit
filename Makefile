.PHONY: build test vet fix check golden

build:
	go build ./...

test:
	go test -race ./...

vet:
	go vet ./...

# Go 1.27 modernizers must leave nothing to rewrite.
fix:
	@out=$$(go fix -diff ./...); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

check: vet fix test

# Rewrite the golden help pages after an intended change; review the diff.
golden:
	CLIKIT_UPDATE=1 go test ./...
