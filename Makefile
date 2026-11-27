# Magnetar build helpers. Binaries are fully static (CGO disabled) and reproducible.
#
#   make build      binary for this machine → bin/
#   make release    everything for Windows and Linux → dist/
#                   (setup .exe, portable .zip, .deb, .tar.gz, SHA256SUMS)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
PKG     := github.com/nikolap994/magnetar
# Optional override of the release signing public key (default: internal/update/key.go).
UPDATE_PUBLIC_KEY ?=
LDFLAGS := -s -w -buildid= -X $(PKG)/internal/engine.Version=$(VERSION)
ifneq ($(UPDATE_PUBLIC_KEY),)
LDFLAGS += -X $(PKG)/internal/update.releasePublicKey=$(UPDATE_PUBLIC_KEY)
endif
GOFLAGS := -trimpath
WINRES  := go run github.com/tc-hib/go-winres@v0.3.3
export CGO_ENABLED := 0

.PHONY: all build run test lint vet fmt icons winres release binaries archives deb installer source-archive checksums sign-checksums clean

all: test build

build: ## Build for the current platform into ./bin
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o bin/magnetar$(shell go env GOEXE) ./cmd/magnetar

run: build ## Build and run
	./bin/magnetar

test: ## Run tests (with the race detector when cgo is available)
	CGO_ENABLED=1 go test -race -count=1 ./... 2>/dev/null || go test -count=1 ./...

vet:
	go vet ./...
	GOOS=windows go vet ./...

fmt:
	gofmt -s -w cmd internal tools web

lint: vet ## gofmt, go vet and golangci-lint (https://golangci-lint.run)
	@test -z "$$(gofmt -s -l cmd internal tools web | tee /dev/stderr)" || (echo "run 'make fmt'" && exit 1)
	golangci-lint run ./...
	GOOS=windows golangci-lint run ./...

icons: ## Regenerate .ico/.png icons from assets/icon.png
	go run ./tools/mkicon

winres: ## Embed icon, manifest and version info into Windows executables
	$(WINRES) simply --icon assets/icon.ico --manifest gui --product-name Magnetar \
		--file-description "Magnetar BitTorrent client" --copyright "Magnetar contributors, MIT License" \
		--file-version "$(VERSION)" --product-version "$(VERSION)" --out cmd/magnetar/rsrc
	$(WINRES) simply --icon assets/icon.ico --manifest gui --product-name "Magnetar Setup" \
		--file-description "Magnetar installer" --copyright "Magnetar contributors, MIT License" \
		--file-version "$(VERSION)" --product-version "$(VERSION)" --out cmd/magnetar-setup/rsrc

release: clean winres binaries archives deb installer source-archive checksums ## Build all release artifacts into ./dist
	@ls -lh dist

binaries:
	@for t in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; extra=""; \
		if [ $$os = windows ]; then ext=.exe; extra="-H windowsgui"; fi; \
		echo "  build  magnetar $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags '$(LDFLAGS) '"$$extra" \
			-o dist/bin/$${os}_$${arch}/magnetar$$ext ./cmd/magnetar || exit 1; \
	done

archives: ## Portable archives (.tar.gz for Linux, .zip for Windows)
	@for t in linux_amd64 linux_arm64 windows_amd64 windows_arm64; do \
		name=magnetar_$(VERSION)_$$t; dir=dist/$$name; mkdir -p $$dir; \
		cp dist/bin/$$t/* README.md LICENSE $$dir/; \
		case $$t in \
			windows*) (cd dist && if command -v zip >/dev/null; then zip -qr $$name.zip $$name; else python3 -m zipfile -c $$name.zip $$name; fi) ;; \
			*) cp packaging/linux/install.sh $$dir/ && tar -C dist -czf dist/$$name.tar.gz $$name ;; \
		esac; \
		rm -rf $$dir; echo "  pack   $$name"; \
	done

deb: ## Debian/Ubuntu packages
	@for arch in amd64 arm64; do \
		echo "  deb    $$(packaging/linux/build-deb.sh dist/bin/linux_$$arch/magnetar $(VERSION) $$arch dist)"; \
	done

installer: ## Windows setup executables (embed magnetar.exe)
	@for arch in amd64 arm64; do \
		gzip -9n < dist/bin/windows_$$arch/magnetar.exe > cmd/magnetar-setup/payload/magnetar.exe.gz; \
		cp LICENSE cmd/magnetar-setup/payload/LICENSE.txt; \
		GOOS=windows GOARCH=$$arch go build $(GOFLAGS) -ldflags '-s -w -buildid= -H windowsgui -X main.Version=$(VERSION)' \
			-o dist/magnetar_$(VERSION)_windows_$${arch}_setup.exe ./cmd/magnetar-setup || exit 1; \
		rm -f cmd/magnetar-setup/payload/magnetar.exe.gz cmd/magnetar-setup/payload/LICENSE.txt; \
		echo "  setup  magnetar_$(VERSION)_windows_$${arch}_setup.exe"; \
	done

source-archive: ## Source tarball with vendored Go modules (offline builds, e.g. Flathub)
	@rm -rf dist/.src && mkdir -p dist/.src
	@git archive --format=tar --prefix=magnetar-$(VERSION)/ HEAD | tar -x -C dist/.src
	@cd dist/.src/magnetar-$(VERSION) && GOFLAGS=-mod=mod go mod vendor
	@tar --sort=name --mtime=@$$(git log -1 --format=%ct HEAD) --owner=0 --group=0 --numeric-owner \
		-C dist/.src -cf - magnetar-$(VERSION) | gzip -9n > dist/magnetar_$(VERSION)_source.tar.gz
	@rm -rf dist/.src
	@echo "  source magnetar_$(VERSION)_source.tar.gz"

checksums:
	@rm -rf dist/bin
	@cd dist && sha256sum *.deb *.tar.gz *.zip *_setup.exe $$(ls *.dmg 2>/dev/null) > SHA256SUMS

sign-checksums: ## Sign dist/SHA256SUMS for the updater (needs MAGNETAR_UPDATE_KEY)
	go run ./tools/signsums sign dist/SHA256SUMS

clean:
	rm -rf bin dist cmd/magnetar/rsrc_windows_*.syso cmd/magnetar-setup/rsrc_windows_*.syso \
		cmd/magnetar-setup/payload/magnetar.exe.gz cmd/magnetar-setup/payload/LICENSE.txt
