.PHONY: build build-web build-go icons icons-check version release test test-cover clean dev

# Build everything: frontend + Go binary
build: build-web build-go

# Build React frontend
build-web:
	cd web && npm ci && npm run build

# Redraw the home-screen icon PNGs from web/public/icon.svg. The PNGs are
# committed, so run this whenever the SVG changes and commit the result.
icons:
	node scripts/make-icons.mjs

# Fail if a committed PNG no longer matches the SVG it is drawn from.
icons-check:
	node scripts/make-icons.mjs --check

# Build Go binary (static, no CGO)
# On Windows the output is clip.exe; on Unix it is clip.
ifeq ($(OS),Windows_NT)
BUILD_OUT := clip.exe
else
BUILD_OUT := clip
endif

# The patch number is the repo's commit count, which only exists at build time —
# stamp it in (see internal/version and scripts/version.mjs). A bare `go build`
# leaves it at 0, which reads as "unstamped development build".
VERSION_PKG := github.com/chinmay28/clip-manager/internal/version
PATCH := $(shell node scripts/version.mjs --patch 2>/dev/null || echo 0)

build-go:
	CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X $(VERSION_PKG).Patch=$(PATCH)" \
		-o $(BUILD_OUT) ./cmd/clip

# Print the version this tree would build as
version:
	@node scripts/version.mjs

# Cross-compile release binaries for all platforms (requires Linux/macOS host)
release: build-web
	./scripts/build-release.sh

# Run all Go unit tests
test:
	go test -count=1 ./...

# Run Go tests with coverage report
test-cover:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -f clip clip.exe coverage.out coverage.html
	rm -rf internal/server/dist
	rm -rf web/node_modules web/dist

# Quick development (build Go only, assumes web is already built)
dev: build-go
	./$(BUILD_OUT) serve --port 8124
