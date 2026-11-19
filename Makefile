.PHONY: build vet fmt lint smoke run sync-bank

build:
	go build .

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# Validate the question bank. Source citations are checked against a
# HyperShift checkout when HS_ROOT is set (or passed as --hs-root).
lint:
	python3 scripts/lint-bank.py

# End-to-end game round over HTTP, including the no-answer-leak assertion.
smoke:
	bash scripts/smoke.sh

run:
	go run .

# Refresh the embedded bank from a HyperShift checkout:
#   make sync-bank HS_ROOT=/path/to/hs-main
sync-bank:
	@test -n "$(HS_ROOT)" || (echo "usage: make sync-bank HS_ROOT=/path/to/hs-main" >&2; exit 1)
	scripts/sync-bank.sh "$(HS_ROOT)"
