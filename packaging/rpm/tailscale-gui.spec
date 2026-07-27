Name:           tailscale-gui
Version:        %{version}
Release:        1%{?dist}
Summary:        Tailscale Linux system tray GUI
License:        MIT
URL:            https://github.com/slimbartfarst/tailscale-gui
BuildArch:      %{buildarch}

Requires:       tailscale >= 1.56
Requires:       libayatana-appindicator
Requires:       libnotify
Requires:       dbus
Recommends:     zenity
Recommends:     xclip

%description
A Linux system tray application for Tailscale that provides a graphical
interface for managing your tailnet.

Features:
  - Peer management with online/offline status
  - Exit node selection
  - Subnet route advertising
  - Taildrop file send/receive with action notifications
  - SSH peer launch with terminal auto-detection
  - Multi-account support
  - Browser-based status dashboard with ping, SSH, and file send

Requires tailscaled to be running. See: https://tailscale.com/download/linux

%install
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/usr/share/applications
mkdir -p %{buildroot}/usr/share/icons/hicolor/256x256/apps
install -m 755 %{_sourcedir}/tailscale-gui        %{buildroot}/usr/bin/tailscale-gui
install -m 644 %{_sourcedir}/tailscale-gui.desktop %{buildroot}/usr/share/applications/tailscale-gui.desktop
install -m 644 %{_sourcedir}/tailscale-gui.png     %{buildroot}/usr/share/icons/hicolor/256x256/apps/tailscale-gui.png

%files
/usr/bin/tailscale-gui
/usr/share/applications/tailscale-gui.desktop
/usr/share/icons/hicolor/256x256/apps/tailscale-gui.png

%post
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -f -t /usr/share/icons/hicolor || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database /usr/share/applications || true
fi

%postun
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database /usr/share/applications || true
fi

%changelog
* %(date "+%a %b %d %Y") CI Build <ci@github.com> - %{version}-1
- Automated build from GitHub Actions
