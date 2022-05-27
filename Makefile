BINDIR := $(CURDIR)/bin
HAS_AIR := $(shell command -v air;)
HAS_GODOC := $(shell command -v godoc;)
HAS_GOLANGCI := $(shell command -v golangci-lint;)

default: dev

dev:
ifndef HAS_AIR
	$(error You must install github.com/cosmtrek/air)
endif
	@air -c .air.toml

lint:
ifndef HAS_GOLANGCI
	$(error You must install github.com/golangci/golangci-lint)
endif
	@golangci-lint run -v -c .golangci.yml && echo "Lint OK"

test:
	@go test -timeout 120s -short -v -race -cover -coverprofile=coverage.out ./...

coverage:
	@go tool cover -func=coverage.out

doc:
ifndef HAS_GODOC
	$(error You must install godoc, run "go get golang.org/x/tools/cmd/godoc")
endif
	@echo "open http://localhost:6060/pkg/github.com/elazarl/goproxy in your browser\n"
	@godoc -http :6060

ci-integration: lint coverage

.PHONY: lint test coverage ci ci-integration
