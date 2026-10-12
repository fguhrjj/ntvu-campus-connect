GO ?= go
GOWINRES ?= $(GO) run github.com/tc-hib/go-winres@v0.3.3
WINDOWS_TEST_DIR := build/windows-test

.PHONY: test vet windows windows-test

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
	GOOS=windows GOARCH=amd64 $(GO) vet ./...

# Embeds resources/winres.json (manifest + icon) and builds the GUI executable.
windows:
	$(GOWINRES) make --in resources/winres.json --arch amd64 --out cmd/campusconnect/rsrc
	GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "-H=windowsgui" -o CampusConnect.exe ./cmd/campusconnect

# Cross-compiles every package's tests and runs them on the Windows host
# (WSL interop); fails if any package fails.
windows-test:
	rm -rf $(WINDOWS_TEST_DIR)
	mkdir -p $(WINDOWS_TEST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) test -c -o $(WINDOWS_TEST_DIR)/ ./...
	@fail=0; for t in $(WINDOWS_TEST_DIR)/*.test.exe; do \
		echo "== $$t"; ./$$t || fail=1; \
	done; exit $$fail
