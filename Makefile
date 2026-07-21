BINARY  := tailscale-gui
CMD     := ./cmd/tailscale-gui

.PHONY: all build run icons tidy vet clean install uninstall

all: icons tidy build

icons:
	python3 scripts/generate_icons.py

tidy:
	go mod tidy

vet:
	go vet ./...

build: icons
	go build -o $(BINARY) $(CMD)

run: build
	./$(BINARY) -v

install: build
	mkdir -p ~/.local/bin
	cp $(BINARY) ~/.local/bin/$(BINARY)
	mkdir -p ~/.config/autostart
	cp packaging/tailscale-gui.desktop ~/.config/autostart/tailscale-gui.desktop
	sed -i "s|%h|$$HOME|g" ~/.config/autostart/tailscale-gui.desktop
	@echo "Installed to ~/.local/bin/$(BINARY)"
	@echo "Autostart entry written to ~/.config/autostart/"

uninstall:
	rm -f ~/.local/bin/$(BINARY)
	rm -f ~/.config/autostart/tailscale-gui.desktop

clean:
	rm -f $(BINARY)
