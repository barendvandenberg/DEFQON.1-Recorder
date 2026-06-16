APP      := defqon-recorder
PKG      := ./cmd/recorder
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
DIST     := dist

# os/arch pairs to cross-compile (pure Go, CGO disabled -> static binaries).
TARGETS := \
	darwin/arm64 \
	darwin/amd64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64

.DEFAULT_GOAL := $(APP)

## build: compile the recorder binary
.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o $(APP) $(PKG)

## run: run the recorder (requires a TTY, yt-dlp and FFmpeg)
.PHONY: run
run: build
	./$(APP)

## test: run unit tests
.PHONY: test
test:
	go test ./...

## vet: static analysis
.PHONY: vet
vet:
	go vet ./...

## fmt: format sources
.PHONY: fmt
fmt:
	gofmt -s -w .

## tidy: ensure dependencies are clean
.PHONY: tidy
tidy:
	go mod tidy

## check: fmt + vet + test
.PHONY: check
check: fmt vet
	go test ./...

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -rf $(APP) bin $(DIST) coverage.txt

## release: cross-compile binaries for mac/linux/windows and bundle yt-dlp + ffmpeg
.PHONY: release
release:
	@mkdir -p $(DIST)
	@for target in $(TARGETS); do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		ext=""; \
		[ $$os = windows ] && ext=".exe"; \
		bin=$(APP)-$$os-$$arch$$ext; \
		stem=$(APP)-$(VERSION)-$$os-$$arch; \
		printf "  -> building %-16s\n" "$$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -o $(DIST)/$$bin $(PKG) || exit 1; \
		rm -rf $(DIST)/$$stem; mkdir -p $(DIST)/$$stem; \
		./scripts/fetch-tools.sh $$os $$arch $(DIST)/$$stem || exit 1; \
		cp $(DIST)/$$bin $(DIST)/$$stem/; \
		cp dq-timetable.json $(DIST)/$$stem/; \
		if [ $$os = windows ]; then \
			( cd $(DIST) && zip -qr $$stem.zip $$stem ); \
		else \
			( cd $(DIST) && tar -czf $$stem.tar.gz $$stem ); \
		fi; \
		rm -rf $(DIST)/$$stem; \
	done
	@echo ""; echo "Release artifacts:"; ls -lh $(DIST)

## docker: build the container image
.PHONY: docker
docker:
	docker build -t $(APP):$(VERSION) .

.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //; s/:/ -> /'
