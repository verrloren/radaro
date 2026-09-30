VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build web web-test web-coverage test lint run clean

build: ## build the radaro binary (embeds web/dist)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o radaro ./cmd/radaro

web: ## rebuild the dashboard into web/dist
	npm --prefix web ci
	npm --prefix web run build

web-test: ## run the dashboard tests
	npm --prefix web test

web-coverage: ## dashboard tests with coverage into web/coverage/lcov.info
	npm --prefix web run coverage

test:
	go test ./...

lint:
	gofmt -l . | grep -v node_modules && exit 1 || true
	go vet ./...

run: build
	./radaro demo

clean:
	rm -f radaro
