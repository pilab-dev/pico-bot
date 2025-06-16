.PHONY: all

BINARY ?= pico-git-summary

all: clean build

clean:
	@echo "Cleaning up the project..."
	@rm -rf bin 

build:
	@echo "Building the project..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags netgo --ldflags="-s -w" -o bin/$(BINARY) main.go
	@echo "Build complete: $(BINARY)"
