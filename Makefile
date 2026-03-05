BINARY     := ciam
BUILD_DIR  := ./cmd/ciam
INSTALL_DIR := $(HOME)/go/bin

.PHONY: build install dev up down clean

## build: compila o binário em ./bin/ciam
build:
	go build -o bin/$(BINARY) $(BUILD_DIR)

## install: instala ciam no GOPATH/bin (adicione ~/go/bin ao PATH)
install:
	go install $(BUILD_DIR)
	@echo "Installed: $(INSTALL_DIR)/$(BINARY)"

## dev: instala + sobe os serviços
dev: install
	docker compose up -d
	@echo "ciam ready. Run: ciam status"

## up: sobe Ollama + API via Docker Compose
up:
	docker compose up -d

## down: derruba os serviços
down:
	docker compose down

## clean: remove binários locais
clean:
	rm -f bin/$(BINARY)

## help: lista os targets disponíveis
help:
	@grep -E '^##' Makefile | sed 's/## //'
