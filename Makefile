SHELL := /usr/bin/env bash

# Build type/family: dev | beta | release
# Default dev so `make build-desktop` produces a dev-grade binary (labMode on,
# developer mode ungated); the semantic targets (release/beta/release-desktop/
# beta-desktop) override BUILD_TYPE with target-scoped assignments.
BUILD_TYPE ?= dev
BUILD_FLAVOR ?= default
# External (public) version + release channel shown to users. Public version
# is the semver (e.g. 1.2.0); channel is stable|beta. Channel defaults from
# BUILD_TYPE; PUBLIC_VERSION reads from the PUBLIC_VERSION file unless
# overridden on the command line (make PUBLIC_VERSION=1.2.0).
PUBLIC_VERSION_FILE := PUBLIC_VERSION
PUBLIC_VERSION ?= $(shell cat $(PUBLIC_VERSION_FILE) 2>/dev/null)
CHANNEL ?= $(if $(filter release,$(BUILD_TYPE)),stable,$(if $(filter beta,$(BUILD_TYPE)),beta,dev))

# Wails production mode: prod tags (desktop,release,production), prod icon/info
# and the `sporemind.exe` name. Plain `build-desktop` builds a dev binary
# (`sporemind-dev.exe`); only the semantic release targets force this on.
WAILS_PRODUCTION ?= false
DESKTOP_WAILS_EXE = $(if $(filter true,$(WAILS_PRODUCTION)),sporemind,sporemind-dev)

BUILD_BIN := build/bin
# Desktop artifacts (sporemind.exe / sporemind-v{ver}.exe) land here for dev and
# dev-release builds; release-desktop overrides DESKTOP_OUT_DIR to RELEASE_DIR so
# build/bin only ever holds dev-grade binaries.
DESKTOP_OUT_DIR ?= $(BUILD_BIN)
RELEASE_DIR := build/release
BACKEND_NAME := sporemind
DESKTOP_NAME := sporemind
DESKTOP_APP_DIR := cmd/sporemind-desktop
DESKTOP_WAILS_BIN := $(DESKTOP_APP_DIR)/build/bin
DESKTOP_STAGING := pkg/web/dist
GEN_CLIENTS_DIR := web/src/gen-clients
VERSION_FILE := version

# Mobile
MOBILE_DIR := mobile
MOBILE_PKG := $(MOBILE_DIR)/package.json
MOBILE_APK_NAME := sporemind

NODE_PKGS := shell theme web

# Build encryption: opt-in garble. Auto-detection is disabled because Windows
# Defender commonly flags obfuscated binaries as potentially unwanted software.
# Use `make OBFUSCATE=true build-desktop` to enable garble.
OBFUSCATE ?= false
GARBLE := $(shell command -v garble 2>/dev/null)
ifneq ($(OBFUSCATE),true)
GARBLE :=
endif
# Default garble flags: symbol-name obfuscation only. Use "-literals -tiny" for
# stronger obfuscation (higher false-positive risk).
GARBLE_FLAGS := -tiny
RELEASE_TAGS := release
SPOREMIND_VERSION_LDFLAGS := -X github.com/qomos-w/sporemind/pkg/version.Version=$(SPOREMIND_VERSION)
BUILD_LDFLAGS = \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.Version=$(SPOREMIND_VERSION) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.BuildType=$(BUILD_TYPE) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.BuildFlavor=$(BUILD_FLAVOR) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.Commit=$(shell git rev-parse --short HEAD) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.Dirty=$(shell git diff --quiet -- . ":(exclude)cmd/sporemind-desktop/build" ":(exclude)version" ":(exclude)tmp" || echo true) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.PublicVersion=$(PUBLIC_VERSION) \
	-X github.com/qomos-w/sporemind/pkg/buildinfo.Channel=$(CHANNEL)
RELEASE_LDFLAGS = -s -w $(SPOREMIND_VERSION_LDFLAGS) $(BUILD_LDFLAGS)

# Go build wrapper for non-dev targets
ifeq ($(GARBLE),)
GO_BUILD = go build -tags $(RELEASE_TAGS) -ldflags '$(RELEASE_LDFLAGS)' -o $(1) $(2)
else
GO_BUILD = garble build $(GARBLE_FLAGS) -tags $(RELEASE_TAGS) -ldflags '$(RELEASE_LDFLAGS)' -o $(1) $(2)
endif

# Read current sporemind version without trailing whitespace
SPOREMIND_VERSION = $(shell cat $(VERSION_FILE) 2>/dev/null | tr -d '[:space:]')

# Export into the environment of every recipe so targets don't have to inline
# `VITE_SPOREMIND_VERSION=... cmd` (POSIX-shell syntax that fails under cmd.exe).
# Deferred (`=`) expansion on purpose: target-scoped assignments (e.g.
# `beta: BUILD_TYPE := beta`, `release: WAILS_PRODUCTION := true`) must reach
# these values when the recipe actually runs — `:=` would freeze the file
# defaults at parse time and leak BUILD_TYPE=release into beta builds.
export VITE_SPOREMIND_VERSION = $(SPOREMIND_VERSION)
export VITE_BUILD_TYPE = $(BUILD_TYPE)
export VITE_BUILD_FLAVOR = $(BUILD_FLAVOR)
export VITE_BUILD_VERSION = $(SPOREMIND_VERSION)
export VITE_WAILS_PRODUCTION = $(WAILS_PRODUCTION)

# Bump the version file by 0.01 (e.g., 0.01 -> 0.02)
define BUMP_VERSION
	@node -e "const fs=require('fs'); const f='$(VERSION_FILE)'; let v=parseFloat(fs.readFileSync(f,'utf8').trim()); v+=0.01; const s=v.toFixed(2); fs.writeFileSync(f,s+'\\n'); console.log('[version] bumped to',s);"
endef

PHONY_SYNC=.PHONY: sync-vendor sync-vendor-check

.PHONY: build build-backend build-desktop build-desktop-core build-desktop-full build-desktop-frontend build-desktop-info build-desktop-icons build-apk build-mobile-asset build-mobile-asset-if-needed copy-mobile-asset build-sdk-asset run run-desktop test test-smoke gen gen-ts gen-schemas gen-schemas-check gen-sdk-schemas gen-schema-ts gen-schema-ts-check sync-wails-bindings clean dev dev-api dev-web dev-desktop dev-all lint check ensure-node-dists validate-envelope release beta dev-build dev-release beta-desktop release-desktop


ensure-node-dists:
	@test -d shell/node_modules && test -n "$$(ls -A shell/node_modules 2>/dev/null)" || (echo "[ensure-node-dists] shell deps missing — installing..." && cd shell && bun install && bun run build)
	@test -d theme/node_modules && test -n "$$(ls -A theme/node_modules 2>/dev/null)" || (echo "[ensure-node-dists] theme deps missing — installing..." && cd theme && bun install && bun run build)
	@test -d $(DESKTOP_STAGING) && test -n "$$(ls -A $(DESKTOP_STAGING) 2>/dev/null)" || (echo "[ensure-node-dists] web dist missing — building..." && cd web && bun install && bun run build)

# Sync vendored @qomos packages from the upstream TAGS pinned in go.mod (not
# the sibling working trees, which may be ahead). Fresh clones build from the
# committed vendor/ trees and never need the siblings. `make sync-vendor-check`
# is the CI drift gate.
sync-vendor:
	bash scripts/sync-vendor.sh

sync-vendor-check:
	bash scripts/sync-vendor.sh --check


# Headless backend is pure Go: pin CGO_ENABLED=0 so the binary is always a
# fully static, dependency-free build regardless of host cgo default. Desktop
# targets are NOT affected (wails needs cgo for ObjC/GTK on macOS/Linux).
# GO_BUILD always passes the release tag, so the backend binary embeds the
# packed SDK zip — build-sdk-asset must run first.
build-backend: build-sdk-asset
	mkdir -p $(BUILD_BIN)
	CGO_ENABLED=0 $(call GO_BUILD,$(BUILD_BIN)/$(BACKEND_NAME),./cmd/sporemind)

build-desktop-frontend:
	node scripts/rmtree.mjs $(DESKTOP_STAGING) web/dist
	cd shell && bun install && bun run build
	cd theme && bun install && bun run build
	$(BUMP_VERSION)
	# `|| true`: bun on Windows may report EPERM copying the in-repo file: deps
	# (shell/theme) whose source dirs contain their own node_modules — the
	# links are still created. `bun run build` below is the real gate.
	cd web && (bun install || true) && bun run build

build-desktop-info:
	@node scripts/update-desktop-info.cjs $(DESKTOP_APP_DIR) $(SPOREMIND_VERSION)

build-desktop-icons:
	@test -d scripts/node_modules && test -n "$$(ls -A scripts/node_modules 2>/dev/null)" || (echo "[build-desktop-icons] Installing icon generator deps..." && cd scripts && npm install)
	node scripts/generate-icons.js

# Desktop build core: everything except the mobile APK asset. Full desktop
# builds (build-desktop/build-desktop-full) embed the APK via
# build-mobile-asset-if-needed; lightweight verification targets (dev-release)
# use this core with the "noapk" tag instead.
build-desktop-core: build-desktop-frontend build-desktop-info build-desktop-icons
	cd $(DESKTOP_APP_DIR) && SPOREMIND_VERSION=$(SPOREMIND_VERSION) wails3 generate bindings -ts -d ../../web/src/bindings
	cd $(DESKTOP_APP_DIR) && \
		SPOREMIND_VERSION=$(SPOREMIND_VERSION) \
		PRODUCTION=$(WAILS_PRODUCTION) \
		LDFLAGS_EXTRA='-s -w $(BUILD_LDFLAGS)' \
		$(if $(GARBLE),GARBLE=true GARBLE_FLAGS="$(GARBLE_FLAGS)") \
		wails3 build $(if $(DESKTOP_TAGS),-tags $(DESKTOP_TAGS))
	mkdir -p $(DESKTOP_OUT_DIR)
	cp -f $(DESKTOP_WAILS_BIN)/$(DESKTOP_WAILS_EXE).exe $(DESKTOP_OUT_DIR)/$(DESKTOP_NAME).exe
	cp -f $(DESKTOP_WAILS_BIN)/$(DESKTOP_WAILS_EXE).exe $(DESKTOP_OUT_DIR)/$(DESKTOP_NAME)-v$(SPOREMIND_VERSION).exe

build-desktop: build-mobile-asset-if-needed build-desktop-core

build-desktop-full: build-mobile-asset build-desktop-core

# Build mobile APK. Auto-increments patch version from mobile/package.json.
# Output: ./build/sporemind-v{X.Y.Z}.apk
# APK loads web from the remote sporemind backend (iframe in mobile/www/index.html),
# not from embedded assets — so we rebuild web/dist + pkg/web/dist first to keep
# the embed source ready for a follow-up `make build-backend` deploy.
build-apk:
	@echo "[build-apk] Installing mobile deps..."
	cd $(MOBILE_DIR) && npm install
	@echo "[build-apk] Preparing mobile www..."
	@mkdir -p $(MOBILE_DIR)/www
	@test -f $(MOBILE_DIR)/www/index.html || (echo "[build-apk] ERROR: $(MOBILE_DIR)/www/index.html not found"; exit 1)
	@echo "[build-apk] Injecting defaults from .env..."
	node scripts/inject-mobile-defaults.mjs
	@echo "[build-apk] Bumping patch version..."
	@node -e "const fs=require('fs'); const p=JSON.parse(fs.readFileSync('$(MOBILE_PKG)')); const v=p.version.split('.'); v[2]=parseInt(v[2]||0)+1; p.version=v.join('.'); fs.writeFileSync('$(MOBILE_PKG)',JSON.stringify(p,null,2)+'\n'); console.log('Version:',p.version);"
	@echo "[build-apk] Ensuring Android platform..."
	@if [ ! -d "$(MOBILE_DIR)/android" ]; then cd $(MOBILE_DIR) && npx cap add android; fi
	@echo "[build-apk] Generating launcher icons from assets/icon.svg..."
	cd $(MOBILE_DIR) && npx @capacitor/assets generate --assetPath ../assets --iconBackgroundColor '#1b1d17' --splashBackgroundColor '#111111' --android
	@echo "[build-apk] Syncing Capacitor..."
	cd $(MOBILE_DIR) && npx cap sync android
	@echo "[build-apk] Building APK..."
	@GRADLE_JAVA=$$(ls -d "/d/Program Files/Eclipse Adoptium"/jdk-21.*-hotspot 2>/dev/null | sort -V | tail -1); \
	if [ -z "$$GRADLE_JAVA" ]; then GRADLE_JAVA=$${JAVA_HOME}; fi; \
	echo "[build-apk] Using JDK: $$GRADLE_JAVA"; \
	if [ -f "$(MOBILE_DIR)/android/gradlew" ]; then \
		cd $(MOBILE_DIR)/android && JAVA_HOME="$$GRADLE_JAVA" ./gradlew clean && JAVA_HOME="$$GRADLE_JAVA" ./gradlew assembleRelease; \
	elif [ -f "$(MOBILE_DIR)/android/gradlew.bat" ]; then \
		cd $(MOBILE_DIR)/android && JAVA_HOME="$$GRADLE_JAVA" ./gradlew.bat clean && JAVA_HOME="$$GRADLE_JAVA" ./gradlew.bat assembleRelease; \
	else \
		echo "[build-apk] ERROR: Gradle wrapper not found. Run 'cd $(MOBILE_DIR) && npx cap add android' first."; exit 1; \
	fi
	@mkdir -p build
	@VER=$$(node -e "console.log(JSON.parse(require('fs').readFileSync('$(MOBILE_PKG)')).version)"); \
	APKDIR="$(MOBILE_DIR)/android/app/build/outputs/apk/release"; \
	SRC="$$APKDIR/app-release.apk"; \
	if [ ! -f "$$SRC" ]; then SRC="$$APKDIR/app-release-unsigned.apk"; fi; \
	DST="build/$(MOBILE_APK_NAME)-v$$VER.apk"; \
	cp "$$SRC" "$$DST"; \
	cp "$$SRC" "build/$(MOBILE_APK_NAME).apk"; \
	echo "[build-apk] Output: $$DST"; \
	echo "[build-apk] Also copied to build/$(MOBILE_APK_NAME).apk (version-less)"

# Copy the compiled APK into the Go embed path used by pkg/mobileassets.
# This is a separate target so `build-desktop` can depend on it without
# rebuilding the APK every time if it is already up to date.
build-mobile-asset: build-apk copy-mobile-asset

build-mobile-asset-if-needed:
	@if [ -f "build/$(MOBILE_APK_NAME).apk" ]; then \
		echo "[build-mobile-asset-if-needed] APK build/$(MOBILE_APK_NAME).apk already exists; skipping APK build"; \
	else \
		$(MAKE) build-apk; \
	fi
	$(MAKE) copy-mobile-asset

copy-mobile-asset:
	@SRC="build/$(MOBILE_APK_NAME).apk"; \
	DST="pkg/mobileassets/sporemind.apk"; \
	if [ ! -f "$$SRC" ]; then \
		echo "[copy-mobile-asset] ERROR: $$SRC not found. Run 'make build-apk' first."; \
		exit 1; \
	fi; \
	mkdir -p pkg/mobileassets; \
	cp -f "$$SRC" "$$DST"; \
	echo "[copy-mobile-asset] Embedded $$SRC -> $$DST"

build: build-backend build-desktop

# The plugin SDK ships in-repo under ./sporemind-plugin-sdk and stays local
# (module github.com/qomos-w/sporemind-plugin-sdk via a directory replace).
SDK_DIR ?= sporemind-plugin-sdk

.PHONY: sdk-dir-check
sdk-dir-check:
	@test -f "$(SDK_DIR)/go.mod" || { echo "[sdk] $(SDK_DIR) is not a sporemind-plugin-sdk checkout (go.mod missing)"; exit 1; }

# Pack the plugin SDK source into pkg/codegen/sdk.zip for release embedding
# (//go:embed sdk.zip in sdk_embed_release.go). Deterministic output: fixed
# timestamps, Deflate, examples/ and dot-files excluded — same rules as the
# vendor-sdk copy. Prints "<sha256> <path> <n> files".
build-sdk-asset: sdk-dir-check
	go run ./cmd/tools/sdkzip -sdk $(SDK_DIR) -out pkg/codegen/sdk.zip

# Semantic build targets
release: BUILD_TYPE := release
release: BUILD_FLAVOR := default
release: WAILS_PRODUCTION := true
release: DESKTOP_TAGS := release
release: build-sdk-asset build

beta: BUILD_TYPE := beta
beta: BUILD_FLAVOR := internal
beta: WAILS_PRODUCTION := true
beta: DESKTOP_TAGS := release
beta: build-sdk-asset build

dev-build: BUILD_TYPE := dev
dev-build: build-backend build-desktop

# Dev binary with the embedded SDK (build tag "devrelease"): same dev
# semantics as build-desktop, but the packed SDK zip is compiled in so the
# binary is self-contained off-repo. Uses the noapk mobile tag: no APK is
# embedded and the gradle chain never runs — this is the lightweight way to
# exercise the release embed → cache extraction → vendor path on a dev machine.
# WAILS_PRODUCTION stays false (same as build-desktop) so the "production" tag
# is NOT injected — DevTools and the right-click context menu stay enabled,
# matching build-desktop. The Makefile copies the output to sporemind.exe
# regardless, so the artifact path is unaffected.
# Artifacts land at the same path as build-desktop (build/bin/sporemind.exe and
# build/bin/sporemind-v{ver}.exe) so a launcher keyed on that path picks up the
# latest dev-release build. Instance isolation comes from the devrelease build
# tag alone — ephemeral gateway port (OS-assigned; the bound address is
# recorded in <dataDir>/gateway.port for same-flavor takeover) + data dir
# .sporemind-devrelease (pkg/config/gateway_flavor_devrelease.go). Startup
# never kills processes, so same-named builds of other flavors are not
# affected.
dev-release: BUILD_TYPE := dev
dev-release: DESKTOP_TAGS := devrelease,noapk
dev-release: build-sdk-asset build-desktop-core

# Beta test build: public version from PUBLIC_VERSION file, channel=beta.
beta-desktop: BUILD_TYPE := beta
beta-desktop: BUILD_FLAVOR := internal
beta-desktop: WAILS_PRODUCTION := true
beta-desktop: DESKTOP_TAGS := release
beta-desktop: build-sdk-asset build-desktop

# Release build: BUILD_TYPE=release (channel stable). All desktop artifacts are
# redirected to build/release/ (nothing lands in build/bin), including the
# public-version-named exe under the public version.
release-desktop: BUILD_TYPE := release
release-desktop: BUILD_FLAVOR := default
release-desktop: WAILS_PRODUCTION := true
release-desktop: DESKTOP_TAGS := release
release-desktop: DESKTOP_OUT_DIR := $(RELEASE_DIR)
release-desktop: build-sdk-asset build-desktop
	cp -f $(RELEASE_DIR)/$(DESKTOP_NAME).exe $(RELEASE_DIR)/$(DESKTOP_NAME)-$(PUBLIC_VERSION)-stable.exe

run: build-backend
	$(BUILD_BIN)/$(BACKEND_NAME)

run-desktop: build-desktop
	$(BUILD_BIN)/$(DESKTOP_NAME).exe


dev:
	@echo "Development targets:"
	@echo "  make dev-api      Run Go API/web host"
	@echo "  make dev-web      Run Vite frontend dev server"
	@echo "  make dev-desktop  Run Wails desktop app in dev mode"
	@echo "  make dev-all      Run API and web dev server together"

dev-api:
	go run -ldflags '$(SPOREMIND_VERSION_LDFLAGS) $(BUILD_LDFLAGS)' ./cmd/sporemind

dev-web: ensure-node-dists
	cd web && bun run dev

dev-desktop: SPOREMIND_DEV_PROXY := http://localhost:5558
dev-desktop: build-desktop-info build-desktop-icons | $(GEN_CLIENTS_DIR)
	-node web/scripts/kill-port.cjs 5558
	# Create a minimal dist placeholder so //go:embed dist in pkg/web/web.go compiles.
	# At runtime the gateway proxies to the Vite dev server (SPOREMIND_DEV_PROXY), so the placeholder is never served.
	mkdir -p pkg/web/dist && echo '<!DOCTYPE html><html><head><meta charset="utf-8"/></head><body>loading...</body></html>' > pkg/web/dist/index.html
	cd $(DESKTOP_APP_DIR) && SPOREMIND_VERSION=$(SPOREMIND_VERSION) SPOREMIND_GATEWAY_ADDR=:18080 SPOREMIND_DEV_PROXY=$(SPOREMIND_DEV_PROXY) SPOREMIND_KEEP_CONSOLE=1 wails3 dev -config ./build/config.yml -port 5558

dev-desktop-debug: SPOREMIND_DEV_PROXY := http://localhost:5558
dev-desktop-debug: build-desktop-info build-desktop-icons | $(GEN_CLIENTS_DIR)
	-node web/scripts/kill-port.cjs 5558
	# Create a minimal dist placeholder so //go:embed dist compiles.
	mkdir -p pkg/web/dist && echo '<!DOCTYPE html><html><head><meta charset="utf-8"/></head><body>loading...</body></html>' > pkg/web/dist/index.html
	cd $(DESKTOP_APP_DIR) && SPOREMIND_VERSION=$(SPOREMIND_VERSION) SPOREMIND_GATEWAY_ADDR=:18080 SPOREMIND_DEV_PROXY=$(SPOREMIND_DEV_PROXY) SPOREMIND_KEEP_CONSOLE=1 WAILS_LOG_LEVEL=debug wails3 dev -config ./build/config.yml -port 5558

# Order-only bootstrap: only runs when gen-clients doesn't exist (fresh
# clone or after `make clean`). Schema changes require explicit `make gen-ts`.
$(GEN_CLIENTS_DIR):
	$(MAKE) gen-ts

dev-all:
	$(MAKE) -j2 dev-api dev-web

gen-schemas:
	node scripts/rmtree.mjs pkg/domain/gen
	go run github.com/qomos-w/spore/cmd/spore-gen-go-types \
		--in schemas \
		--out-dir pkg/domain/gen \
		--package gen

gen-sdk-schemas: sdk-dir-check
	node scripts/rmtree.mjs $(SDK_DIR)/gen
	go run github.com/qomos-w/spore/cmd/spore-gen-go-types \
		--in schemas \
		--out-dir $(SDK_DIR)/gen \
		--package gen \
		--no-registry-init
	# Slim gen/ to only the plugin contract types (app.gen.go, plugin.gen.go).
	# Delete the registry and all service projection files — the SDK core
	# only references 9 contract types defined in these two files.
	node -e "const fs=require('fs'),p=require('path'),d='$(SDK_DIR)/gen';for(const f of fs.readdirSync(d))if(f.endsWith('.gen.go')&&f!=='app.gen.go'&&f!=='plugin.gen.go')fs.unlinkSync(p.join(d,f))"
	cd $(SDK_DIR) && go mod tidy

gen-schemas-check: gen-schemas
	@if ! git diff --quiet -- pkg/domain/gen; then \
		echo "[gen-schemas-check] pkg/domain/gen is out of sync with schemas/. Run 'make gen-schemas' and commit."; \
		git --no-pager diff --stat -- pkg/domain/gen; \
		exit 1; \
	fi

gen-schema-ts:
	node scripts/gen-schema-ts.mjs

gen-icon-catalog:
	node --experimental-strip-types web/scripts/gen-icon-catalog.ts

gen-icon-catalog-check:
	@node --experimental-strip-types web/scripts/gen-icon-catalog.ts --check

gen-schema-ts-check: gen-schema-ts
	@if ! git diff --quiet -- web/src/gen-types; then \
		echo "[gen-schema-ts-check] web/src/gen-types is out of sync with schemas/. Run 'make gen-schema-ts' and commit."; \
		git --no-pager diff --stat -- web/src/gen-types; \
		exit 1; \
	fi

# Regenerate Wails v3 frontend bindings (TS) from Go service signatures.
# Outputs to web/src/bindings/. Run after changing Go service methods, or
# automatically as part of `make build-desktop`.
sync-wails-bindings:
	cd $(DESKTOP_APP_DIR) && wails3 generate bindings -ts -d ../../web/src/bindings

gen-static-fragment:
	go run ./cmd/sporemind-gen-static-fragment -o gen/static_schema_fragment.json

gen-static-fragment-check: gen-static-fragment
	@if ! git diff --quiet -- gen/static_schema_fragment.json; then \
		echo "[gen-static-fragment-check] gen/static_schema_fragment.json is out of sync with schemas/. Run 'make gen-static-fragment' and commit."; \
		git --no-pager diff --stat -- gen/static_schema_fragment.json; \
		exit 1; \
	fi

# Regenerate gmanifest.json from the runtime actor/callable surface.
gen-manifest:
	go run ./cmd/sporemind-gen-manifest -o gen/gmanifest.json

# Regenerate TypeScript RPC clients from the runtime manifest.
gen-clients: gen-manifest
	node scripts/rmtree.mjs $(GEN_CLIENTS_DIR)
	MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' \
	go run github.com/qomos-w/gospore/cmd/gospore-gen-ts \
		-in gen/gmanifest.json \
		-out $(GEN_CLIENTS_DIR) \
		--visibility public,admin \
		--header "// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen"

gen-ts: gen-static-fragment gen-schema-ts gen-clients

gen-manifest-check: gen-clients
	@if ! git diff --quiet -- gen/gmanifest.json $(GEN_CLIENTS_DIR); then \
		echo "[gen-manifest-check] gen/gmanifest.json or $(GEN_CLIENTS_DIR) is out of sync with the runtime surface. Run 'make gen-ts' and commit."; \
		git --no-pager diff --stat -- gen/gmanifest.json $(GEN_CLIENTS_DIR); \
		exit 1; \
	fi

gen-sdk-check: gen-sdk-schemas
	@if ! git -C $(SDK_DIR) diff --quiet -- gen; then \
		echo "[gen-sdk-check] $(SDK_DIR)/gen is out of sync with schemas/. Run 'make gen-sdk-schemas' and commit in the SDK repo."; \
		git -C $(SDK_DIR) --no-pager diff --stat -- gen; \
		exit 1; \
	fi

# Reassign schema ._N base ids so each module (filename stem minus .partN)
# owns a contiguous id segment sized to its struct count plus a proportional
# gap. Dry-run: go run ./cmd/sporemind-renumber-schemas
renumber-schemas:
	go run ./cmd/sporemind-renumber-schemas -w

gen: gen-schemas gen-ts gen-sdk-schemas

test:
	go test ./...

lint: ensure-node-dists
	go vet -unsafeptr=false ./pkg/desktop/...
	go vet $$(go list ./... | grep -v 'github.com/qomos-w/sporemind/pkg/desktop')
	cd web && npx tsc --noEmit

check: gen-static-fragment-check gen-schemas-check gen-schema-ts-check gen-icon-catalog-check gen-manifest-check gen-sdk-check lint test

test-smoke: build-backend gen ensure-node-dists
	@echo "Installing web deps & type-checking smoke ..."
	cd web && bun install && npx tsc -p tsconfig.smoke.json
	@echo "Starting sporemind backend on :18080 ..."
	@$(BUILD_BIN)/$(BACKEND_NAME) & \
	BACKEND_PID=$$!; \
	cd web && npx tsx src/test-smoke.ts http://localhost:18080; \
	TEST_EXIT=$$?; \
	kill $$BACKEND_PID 2>/dev/null || true; \
	exit $$TEST_EXIT

validate-envelope:
	@if [ -z "$(ENVELOPE)" ]; then \
		echo "Usage: make validate-envelope ENVELOPE=PROJECT-GRAPH-TRIAL-V*.md"; \
		exit 1; \
	fi
	go run scripts/validate-envelope.go $(ENVELOPE)

clean:
	node scripts/rmtree.mjs gen build $(DESKTOP_APP_DIR)/build/bin $(DESKTOP_APP_DIR)/wails_windows_*.syso pkg/web/dist.enc pkg/web/enc_key.gen.go
