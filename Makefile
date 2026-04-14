BINARY  := sundial
PREFIX  := /usr/local/bin

.PHONY: build install uninstall test vet clean

build:
	go build -o $(BINARY) .

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
