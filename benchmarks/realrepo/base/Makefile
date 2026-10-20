.PHONY: check test vet fmt pricecheck

check: fmt vet test

fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

test:
	go test ./...

# Reads each vendor's pricing page; this snapshot carries no network client.
pricecheck:
	@echo "pricecheck: reaches each vendor's pricing page and needs the network; not available in this snapshot" >&2
	@exit 2
