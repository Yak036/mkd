# MKD — tareas de desarrollo y release.
#
#	make build      compila el binario local con versión desde git
#	make test       ejecuta toda la suite
#	make lint       go vet + comprobación de formato
#	make install    instala en ~/.local/bin
#	make snapshot   genera artefactos de release sin publicar (goreleaser)

BINARY      := mkd
MODULE      := mkd
CMD_DIR     := .
BIN_DIR     := bin
INSTALL_DIR ?= $(HOME)/.local/bin

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build install uninstall test test-race cover lint fmt vet run clean snapshot release-dry help

all: build

## build: compila el binario en ./mkd
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD_DIR)

## install: instala el binario en $(INSTALL_DIR)
install: build
	@mkdir -p $(INSTALL_DIR)
	@install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "instalado en $(INSTALL_DIR)/$(BINARY)"

## uninstall: elimina el binario instalado
uninstall:
	@rm -f $(INSTALL_DIR)/$(BINARY)
	@echo "eliminado $(INSTALL_DIR)/$(BINARY)"

## test: ejecuta la suite completa
test:
	go test ./... -count=1

## test-race: suite con detector de carreras
test-race:
	go test ./... -race -count=1

## cover: informe de cobertura
cover:
	go test ./... -coverprofile=coverage.out -count=1
	go tool cover -func=coverage.out | tail -1

## lint: vet + gofmt + goimports si está disponible
lint: vet fmt

## vet: análisis estático
vet:
	go vet ./...

## fmt: comprueba el formato (falla si hay archivos sin formatear)
fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "archivos sin gofmt:"; echo "$$unformatted"; exit 1; \
	fi

## run: abre la TUI en modo desarrollo
run:
	go run $(CMD_DIR) chat

## clean: elimina artefactos locales
clean:
	rm -rf $(BINARY) $(BIN_DIR) dist coverage.out

## snapshot: release local sin publicar
snapshot:
	goreleaser release --snapshot --clean

## release-dry: valida la configuración de goreleaser
release-dry:
	goreleaser check

## help: lista estas tareas
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
