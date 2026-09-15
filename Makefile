VERSION ?= $(shell git describe --tags --match 'v*' 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

RELEASE_ARCHS := x86_64 aarch64
TAR_PREFIX := omaseal-linux
GOARCH_x86_64 := amd64
GOARCH_aarch64 := arm64

.PHONY: all build build-all test clean release-bump

all: build

# A single `make build` produces static linux/amd64 and linux/arm64 archives.
build: build-all

build-all: $(RELEASE_ARCHS)

$(RELEASE_ARCHS):
	mkdir -p dist/$(TAR_PREFIX)-$@
	cd engine && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH_$@) go build -ldflags "$(LDFLAGS)" -o ../dist/$(TAR_PREFIX)-$@/omaseal .
	cp -f BarWidget.qml Panel.qml manifest.json README.md LICENSE dist/$(TAR_PREFIX)-$@/
	tar -czf dist/$(TAR_PREFIX)-$@.tar.gz -C dist $(TAR_PREFIX)-$@

test:
	cd engine && go test ./...

# Post-release pin bump: `make release-bump TAG=v0.2.3` (add --allow-unsigned
# via BUMP_FLAGS for unsigned releases). Needs makepkg — Arch host only.
release-bump:
	$(if $(TAG),,$(error release-bump requires TAG=vX.Y.Z))
	packaging/aur/bump.sh $(TAG) $(BUMP_FLAGS)

clean:
	rm -rf dist/ engine/omaseal
