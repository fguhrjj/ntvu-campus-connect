GO ?= go

.PHONY: test vet

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
	GOOS=windows GOARCH=amd64 $(GO) vet ./...
