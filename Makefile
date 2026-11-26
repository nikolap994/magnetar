# Magnetar build helpers. Binaries are fully static (CGO disabled) and reproducible.
#
#   make build      binary for this machine → bin/
#   make release    everything for Windows and Linux → dist/
#                   (setup .exe, portable .zip, .deb, .tar.gz, SHA256SUMS)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
PKG     := github.com/nikolap994/magnetar
# Public half of the release signing key (repository variable UPDATE_PUBLIC_KEY).
# Builds without it still run but never install updates.
UPDATE_PUBLIC_KEY ?=
LDFLAGS := -s -w -buildid= -X $(PKG)/internal/engine.Version=$(VERSION) \
	-X $(PKG)/internal/update.releasePublicKey=$(UPDATE_PUBLIC_KEY)
GOFLAGS := -trimpath
WINRES  := go run github.com/tc-hib/go-winres@v0.3.3
export CGO_ENABLED := 0

.PHONY: all build run test lint vet fmt icons winres release binaries archives deb installer checksums sign-checksums clean

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
	gofmt -s -w .

lint: vet
	@test -z "$$(gofmt -s -l . | tee /dev/stderr)" || (echo "run 'make fmt'" && exit 1)

icons: ## Regenerate .ico/.png icons from assets/icon.png
	go run ./tools/mkicon

winres: ## Embed icon, manifest and version info into Windows executables
	$(WINRES) simply --icon assets/icon.ico --manifest gui --product-name Magnetar \
		--file-description "Magnetar BitTorrent client" --copyright "Magnetar contributors, MIT License" \
		--file-version "$(VERSION)" --product-version "$(VERSION)" --out cmd/magnetar/rsrc
	$(WINRES) simply --icon assets/icon.ico --manifest gui --product-name "Magnetar Setup" \
		--file-description "Magnetar installer" --copyright "Magnetar contributors, MIT License" \
		--file-version "$(VERSION)" --product-version "$(VERSION)" --out cmd/magnetar-setup/rsrc

release: clean winres binaries archives deb installer checksums ## Build all release artifacts into ./dist
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

checksums:
	@rm -rf dist/bin
	@cd dist && sha256sum *.deb *.tar.gz *.zip *_setup.exe > SHA256SUMS

sign-checksums: ## Sign dist/SHA256SUMS for the updater (needs MAGNETAR_UPDATE_KEY)
	go run ./tools/signsums sign dist/SHA256SUMS

clean:
	rm -rf bin dist cmd/magnetar/rsrc_windows_*.syso cmd/magnetar-setup/rsrc_windows_*.syso \
		cmd/magnetar-setup/payload/magnetar.exe.gz cmd/magnetar-setup/payload/LICENSE.txt
