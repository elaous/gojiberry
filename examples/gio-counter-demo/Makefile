# gio-counter-demo dev workflow. Separate Go module (like
# ../canvas-app): replace-ing github.com/elaous/gojiberry with the
# local checkout so it can depend on gioui.org without adding it to
# the root module.
.PHONY: run
run: generate
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/goxc examples/gio-counter-demo

# test runs headless -- no display required (see goxgio's own doc
# comment on the real Gio GPU backend it exercises). run needs a real
# display; it's not part of test.
.PHONY: test
test: generate
	go test ./...

# build-* targets package this demo for a real target platform via
# gogio (gioui.org/cmd/gogio, a tool dependency -- see go.mod's own
# `tool` line, added via `go get -tool`), not plain `go build`, except
# build-linux: Linux is the one target gogio doesn't have at all (no
# `-target linux` -- see `go tool gogio -h`), since a Gio program is
# already a plain, unpackaged Go binary there; every other target needs
# gogio for real platform packaging (an .app/.apk/.ipa bundle, an icon
# resource embedded in the .exe, the wasm module's own support files).
#
# Verified by hand in this sandbox: build-web, build-linux, and
# build-windows all succeed with no extra SDK install (Windows
# cross-compiles cleanly -- pure Go plus github.com/akavel/rsrc for the
# icon resource, no mingw/MSVC needed). NOT verified here, and not
# expected to work in any sandbox lacking the real platform tooling:
# build-android (fails fast with "please set ANDROID_HOME" -- needs the
# real Android SDK/NDK, a genuine gap left for whoever next has that
# installed, not attempted here) and build-macos/build-ios (both need a
# real macOS host with Xcode -- confirmed by hand: build-macos fails
# inside cgo's own arch-selection step, `clang: error: unsupported
# option '-arch'`; build-ios fails outright on a missing `xcrun`). The
# macOS/iOS gap is the same one already tracked in OnDesign's own
# mobile-backend ROADMAP entry: deferred to a real Mac, not this
# sandbox.
#
# -arch/-minsdk/-targetsdk/signing are all left at gogio's own defaults
# for now -- the actual per-architecture build matrix (arm64 vs amd64
# vs...) is explicitly out of scope for this pass, to be tackled
# separately.
DIST := dist

.PHONY: build-web
build-web: generate
	go tool gogio -target js -o $(DIST)/web .

.PHONY: build-linux
build-linux: generate
	go build -o $(DIST)/gio-counter-demo-linux .

.PHONY: build-windows
build-windows: generate
	go tool gogio -target windows -o $(DIST)/gio-counter-demo-windows.exe .

.PHONY: build-macos
build-macos: generate
	go tool gogio -target macos -o $(DIST)/gio-counter-demo-macos.app .

.PHONY: build-android
build-android: generate
	go tool gogio -target android -o $(DIST)/gio-counter-demo-android.apk .

.PHONY: build-ios
build-ios: generate
	go tool gogio -target ios -o $(DIST)/gio-counter-demo-ios.ipa .

# build-desktop/build-mobile/build-all are best-effort umbrellas, not a
# guarantee every target succeeds in every environment -- see the doc
# comment above for exactly which targets need SDKs this sandbox
# doesn't have.
.PHONY: build-desktop
build-desktop: build-linux build-windows build-macos

.PHONY: build-mobile
build-mobile: build-android build-ios

.PHONY: build-all
build-all: build-web build-desktop build-mobile

.PHONY: clean
clean:
	rm -rf $(DIST) *.syso
