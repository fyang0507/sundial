BINARY    := sundial
PREFIX    := $(shell go env GOPATH)/bin
DATA_REPO := $(shell grep '^data_repo:' config.yaml 2>/dev/null | sed 's/data_repo: *"\([^"]*\)".*/\1/' | sed "s|~|$$HOME|")

.PHONY: build install uninstall test vet clean

build:
	go build -o $(BINARY) .
ifdef DATA_REPO
	@mkdir -p "$(DATA_REPO)/.agents/skills"
	@cp -R skills/sundial "$(DATA_REPO)/.agents/skills/"
	@echo "skills/sundial copied to $(DATA_REPO)/.agents/skills/sundial"
endif

install: build
	install -d $(PREFIX)
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)
	@echo "$(BINARY) installed to $(PREFIX)/$(BINARY)"

uninstall:
	rm -f $(PREFIX)/$(BINARY)
	@echo "$(BINARY) removed from $(PREFIX)"

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
