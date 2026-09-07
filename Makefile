VERSION ?= $(shell git describe --tags --match 'v*' 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

RELEASE_ARCHS := x86_64 aarch64
TAR_PREFIX := omaseal-linux
GOARCH_x86_64 := amd64
GOARCH_aarch64 := arm64

.PHONY: all build build-all test clean

all: build

build:
	cd engine && CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o omaseal .

build-all: $(RELEASE_ARCHS)

$(RELEASE_ARCHS):
	mkdir -p dist/$(TAR_PREFIX)-$@
	cd engine && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH_$@) go build -ldflags "$(LDFLAGS)" -o ../dist/$(TAR_PREFIX)-$@/omaseal .
	cp -f BarWidget.qml Panel.qml manifest.json README.md LICENSE dist/$(TAR_PREFIX)-$@/
	tar -czf dist/$(TAR_PREFIX)-$@.tar.gz -C dist $(TAR_PREFIX)-$@

test:
	cd engine && go test ./...

clean:
	rm -rf dist/ engine/omaseal
