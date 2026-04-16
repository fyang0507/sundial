BINARY    := sundial
PREFIX    := $(shell go env GOPATH)/bin
CONFIG    := $(CURDIR)/config.yaml
DATA_REPO := $(shell grep '^data_repo:' config.yaml 2>/dev/null | sed 's/data_repo: *"\([^"]*\)".*/\1/' | sed "s|~|$$HOME|")

.PHONY: build install uninstall test vet clean start stop restart

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

# Start the daemon in the background using this repo's config.yaml.
# Use `make start launchd=1` to also register with launchd for auto-start on login.
start: install
	@if sundial health --json 2>/dev/null | grep -q '"daemon_running":true'; then \
		echo "daemon is already running"; \
	else \
		SUNDIAL_CONFIG="$(CONFIG)" sundial daemon &>/dev/null & \
		sleep 1; \
		echo "daemon started (pid $$!)"; \
	fi
ifdef launchd
	SUNDIAL_CONFIG="$(CONFIG)" sundial install
endif
	@SUNDIAL_CONFIG="$(CONFIG)" sundial health

# Stop the daemon.
stop:
	@pkill -f "sundial daemon" 2>/dev/null && echo "daemon stopped" || echo "daemon was not running"

# Restart the daemon.
restart: stop
	@sleep 1
	$(MAKE) start

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
