BINARY   := sundial
PREFIX   := $(shell go env GOPATH)/bin
DEV_YAML := $(CURDIR)/sundial.config.dev.yaml
DATA_REPO := $(shell grep '^data_repo_path:' "$(DEV_YAML)" 2>/dev/null | sed 's/data_repo_path: *"\([^"]*\)".*/\1/' | sed "s|~|$$HOME|")

.PHONY: build install uninstall test vet clean start stop restart setup

build:
	go build -o $(BINARY) .

install: build
	install -d $(PREFIX)
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)
	@echo "$(BINARY) installed to $(PREFIX)/$(BINARY)"

uninstall:
	rm -f $(PREFIX)/$(BINARY)
	@echo "$(BINARY) removed from $(PREFIX)"

# Scaffold the data repo (workspace.yaml, sundial/config.yaml, skills sync).
# Idempotent — safe to rerun.
setup: install
ifndef DATA_REPO
	$(error data_repo_path not set in $(DEV_YAML); copy sundial.config.dev.yaml.example and fill it in)
endif
	SUNDIAL_DATA_REPO="$(DATA_REPO)" sundial setup

# Start the daemon in the background using the data repo resolved from
# sundial.config.dev.yaml. Use `make start launchd=1` to also register with
# launchd for auto-start on login.
start: setup
	@if sundial health --json 2>/dev/null | grep -q '"daemon_running":true'; then \
		echo "daemon is already running"; \
	else \
		SUNDIAL_DATA_REPO="$(DATA_REPO)" sundial daemon &>/dev/null & \
		sleep 1; \
		echo "daemon started (pid $$!)"; \
	fi
ifdef launchd
	SUNDIAL_DATA_REPO="$(DATA_REPO)" sundial install
endif
	@SUNDIAL_DATA_REPO="$(DATA_REPO)" sundial health

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
