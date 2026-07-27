# Setting up tailscale-gui on GitHub

## 1. Rename the module

Replace every occurrence of `slimbartfarst` with your actual GitHub username:

```bash
# On Linux/macOS:
grep -rn "yourname" --include="*.go" --include="*.mod" --include="*.yml" .
# Then replace:
find . -type f \( -name "*.go" -o -name "*.mod" -o -name "*.yml" \) \
  -exec sed -i 's/yourname/YOUR_GITHUB_USERNAME/g' {} +
```

## 2. Create the GitHub repo

```bash
gh repo create tailscale-gui --public --description "Tailscale Linux system tray GUI"
# or create it at https://github.com/new
```

## 3. First push

```bash
cd tailscale-gui
git init
git add .
git commit -m "Initial commit"
git branch -M main
git remote add origin https://github.com/YOUR_GITHUB_USERNAME/tailscale-gui.git
git push -u origin main
```

The push triggers the **Build** workflow automatically. Check progress at:
`https://github.com/YOUR_GITHUB_USERNAME/tailscale-gui/actions`

## 4. Enable release creation

Go to **Settings → Actions → General → Workflow permissions** and set:
- ✅ Read and write permissions
- ✅ Allow GitHub Actions to create and approve pull requests

This is required for the `release` job to create GitHub Releases.

## 5. Create a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

The `release` job runs automatically on `v*` tags and creates a GitHub Release
with the `.deb`, `.rpm`, and `.AppImage` files attached.

## 6. What the CI produces

| File | Architectures |
|---|---|
| `tailscale-gui_0.1.0_amd64.deb` | amd64 |
| `tailscale-gui_0.1.0_arm64.deb` | arm64 |
| `tailscale-gui-0.1.0-1.x86_64.rpm` | x86_64 |
| `tailscale-gui-0.1.0-1.aarch64.rpm` | aarch64 |
| `tailscale-gui-0.1.0-x86_64.AppImage` | amd64 |

## 7. Replace placeholder icons

The build currently embeds solid-colour placeholder PNGs. Replace them with
real 32×32 artwork before releasing publicly:

```
assets/icons/connected.png     # green — used when Tailscale is running
assets/icons/disconnected.png  # grey  — used when stopped/disconnected
assets/icons/connecting.png    # amber — used while connecting
assets/icons/warning.png       # red   — used on error/needs-login
```

Then commit and push; the icons are embedded into the binary via `go:embed`.

## 8. Personalise the package metadata

Update these files with your real details before tagging a release:

- `packaging/deb/control` — Maintainer field
- `packaging/rpm/tailscale-gui.spec` — (maintainer info is in the %changelog)
- `.github/workflows/build.yml` — `MAINTAINER`, `HOMEPAGE` env vars at the top
- `go.mod` — module path after replacing `slimbartfarst`

## 9. Prerequisites for end users

Users installing the package need:

```bash
# Install tailscaled (if not already)
curl -fsSL https://tailscale.com/install.sh | sh

# Grant operator permission (run once)
sudo tailscale set --operator=$USER

# GNOME users: install AppIndicator extension
# https://extensions.gnome.org/extension/615/appindicator-support/

# Optional: install zenity for file picker and route dialogs
sudo apt install zenity   # Debian/Ubuntu
sudo dnf install zenity   # Fedora
```
