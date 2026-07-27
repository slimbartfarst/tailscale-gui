BINARY  := tailscale-gui
CMD     := ./cmd/tailscale-gui
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")

.PHONY: all build run icons tidy vet clean install uninstall deb rpm appimage

all: icons tidy build

icons:
	python3 scripts/generate_icons.py

tidy:
	go mod tidy

vet:
	go vet ./...

build: icons
	go build -ldflags="-s -w -X main.Version=$(VERSION)" -o $(BINARY) $(CMD)

run: build
	./$(BINARY) -v

## test: run pure-logic unit tests (no display required)
test:
	go test -v ./internal/account/... ./internal/picker/... ./internal/routes/... ./internal/notify/... ./internal/ssh/...

## deb: build a .deb package (requires dpkg-deb)
deb: build
	@mkdir -p dist/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN
	@mkdir -p dist/deb/$(BINARY)_$(VERSION)_amd64/usr/bin
	@mkdir -p dist/deb/$(BINARY)_$(VERSION)_amd64/usr/share/applications
	@mkdir -p dist/deb/$(BINARY)_$(VERSION)_amd64/usr/share/icons/hicolor/256x256/apps
	install -m 755 $(BINARY)                    dist/deb/$(BINARY)_$(VERSION)_amd64/usr/bin/$(BINARY)
	sed 's|Exec=.*|Exec=/usr/bin/$(BINARY)|'    packaging/tailscale-gui.desktop \
	  > dist/deb/$(BINARY)_$(VERSION)_amd64/usr/share/applications/$(BINARY).desktop
	cp assets/icons/connected.png               dist/deb/$(BINARY)_$(VERSION)_amd64/usr/share/icons/hicolor/256x256/apps/$(BINARY).png
	@SIZE=$$(du -sk dist/deb/$(BINARY)_$(VERSION)_amd64/usr | cut -f1); \
	 sed -e "s/VERSION_PLACEHOLDER/$(VERSION)/" \
	     -e "s/ARCH_PLACEHOLDER/amd64/" \
	     -e "s/SIZE_PLACEHOLDER/$${SIZE}/" \
	     packaging/deb/control \
	   > dist/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/control
	install -m 755 packaging/deb/postinst  dist/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/postinst
	install -m 755 packaging/deb/postrm    dist/deb/$(BINARY)_$(VERSION)_amd64/DEBIAN/postrm
	dpkg-deb --build --root-owner-group dist/deb/$(BINARY)_$(VERSION)_amd64 \
	  dist/$(BINARY)_$(VERSION)_amd64.deb
	@echo "Built: dist/$(BINARY)_$(VERSION)_amd64.deb"

## rpm: build a .rpm package (requires rpmbuild — use Docker on non-Fedora)
rpm: build
	@mkdir -p dist
	rpmdev-setuptree
	cp $(BINARY)                                              ~/rpmbuild/SOURCES/$(BINARY)
	sed 's|Exec=.*|Exec=/usr/bin/$(BINARY)|' packaging/tailscale-gui.desktop \
	  >                                                       ~/rpmbuild/SOURCES/$(BINARY).desktop
	cp assets/icons/connected.png                             ~/rpmbuild/SOURCES/$(BINARY).png
	rpmbuild -bb \
	  --define "version $(VERSION)" \
	  --define "buildarch x86_64" \
	  --target x86_64 \
	  packaging/rpm/$(BINARY).spec
	cp ~/rpmbuild/RPMS/x86_64/$(BINARY)-$(VERSION)-1.x86_64.rpm dist/
	@echo "Built: dist/$(BINARY)-$(VERSION)-1.x86_64.rpm"

## appimage: build an AppImage (requires appimagetool in PATH)
appimage: build
	@mkdir -p build/appimage/$(BINARY).AppDir/usr/bin
	@mkdir -p build/appimage/$(BINARY).AppDir/usr/share/applications
	@mkdir -p build/appimage/$(BINARY).AppDir/usr/share/icons/hicolor/256x256/apps
	install -m 755 $(BINARY)                   build/appimage/$(BINARY).AppDir/usr/bin/$(BINARY)
	install -m 755 packaging/appimage/AppRun   build/appimage/$(BINARY).AppDir/AppRun
	cp packaging/appimage/$(BINARY).desktop    build/appimage/$(BINARY).AppDir/$(BINARY).desktop
	cp packaging/appimage/$(BINARY).desktop    build/appimage/$(BINARY).AppDir/usr/share/applications/
	cp assets/icons/connected.png              build/appimage/$(BINARY).AppDir/$(BINARY).png
	cp assets/icons/connected.png             build/appimage/$(BINARY).AppDir/usr/share/icons/hicolor/256x256/apps/$(BINARY).png
	ARCH=x86_64 appimagetool build/appimage/$(BINARY).AppDir \
	  dist/$(BINARY)-$(VERSION)-x86_64.AppImage
	@echo "Built: dist/$(BINARY)-$(VERSION)-x86_64.AppImage"

install: build
	mkdir -p ~/.local/bin
	cp $(BINARY) ~/.local/bin/$(BINARY)
	mkdir -p ~/.config/autostart
	cp packaging/tailscale-gui.desktop ~/.config/autostart/tailscale-gui.desktop
	sed -i "s|%h|$$HOME|g" ~/.config/autostart/tailscale-gui.desktop
	@echo "Installed to ~/.local/bin/$(BINARY)"

uninstall:
	rm -f ~/.local/bin/$(BINARY)
	rm -f ~/.config/autostart/tailscale-gui.desktop

clean:
	rm -f $(BINARY)
	rm -rf dist/ build/

