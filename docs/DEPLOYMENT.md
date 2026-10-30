# Deployment and release guide

Repository: https://github.com/desirevolution/soundboard

## Normal deployment (`compose.yml`)

Pull the published GHCR image for `linux/amd64` or `linux/arm64`:

```bash
mkdir -p sounds
PUID=$(id -u) PGID=$(id -g) docker compose up -d
```

The normal Compose service uses `ghcr.io/desirevolution/soundboard:latest` and binds **`0.0.0.0:8080`** by default, so it is reachable via `http://HOST:8080/` (Admin: `/admin/`). **This exposes an unauthenticated service** unless you restrict network access separately. Always use a trusted proxy/authentication and/or a firewall. For a reverse proxy on the same machine, `SOUNDBOARD_BIND=127.0.0.1` is an optional safer override. For a containerized proxy, consider omitting `ports` and using an internal shared network.

Environment variables for Compose:

| Variable | Default | Purpose |
| --- | --- | --- |
| `SOUNDBOARD_IMAGE` | `ghcr.io/desirevolution/soundboard:latest` | Published image (override with a fixed tag such as `:0.1.1` to pin a release) |
| `SOUNDBOARD_BIND` | `0.0.0.0` | Host bind interface |
| `SOUNDBOARD_PORT` | `8080` | Host port |
| `PUID` / `PGID` | `1000` / `1000` | Host user/group that own `./sounds` |

A package must actually exist in GHCR before Compose can pull it. Publish a GitHub Release with a `vMAJOR.MINOR.PATCH` tag to create `latest` and versioned tags. Re-running `docker compose pull && docker compose up -d` updates the image.

The `./sounds` mount is read/write and must be owned by the configured UID/GID. Back up `sounds/metadata.json` and `sounds/groups.json` together with the audio files.

## Development (`compose.dev.yml`)

```bash
mkdir -p sounds
PUID=$(id -u) PGID=$(id -g) docker compose -f compose.dev.yml up -d --build
```

Builds the local checkout and binds **`127.0.0.1:8080`** so it is reachable only from the host. Stop the normal Compose service first if both would use the same port or use a different `SOUNDBOARD_PORT`.

For a non-container development run, install Go 1.23+ and `ffprobe`, then run `go run . -sounds ./sounds -addr :8080`. `soundboard -version` prints `dev` for local builds; published release binaries display their GitHub tag.

## GitHub Actions and releases (GitHub UI only)

The workflow runs Go tests and vet on pull requests, pushes to `main` and published Releases. Pushes to `main` publish `main` and `sha-…` multi-architecture images, whose embedded version is `dev`.

To publish the next version, **no source file or commit needs a version bump**:

1. Push the desired code and workflow changes to `main`.
2. On GitHub, open **Releases → Draft a new release**.
3. In **Choose a tag**, enter a new tag such as `v0.1.1`, choose **Create new tag** and target `main`.
4. Publish the release (not just save a draft).
5. Wait for **Build and release** to complete. The workflow validates the tag format `vMAJOR.MINOR.PATCH`, embeds it in the Go binaries, publishes GHCR tags `0.1.1`, `0.1` and `latest` for AMD64/ARM64, and attaches Linux AMD64/ARM64 archives and SHA256 checksums.

The release tag is the **single source of truth**. No tracked `VERSION` file is necessary. GitHub Actions uses the repository `GITHUB_TOKEN`, with `packages: write` for GHCR and `contents: write` for release attachments. The binary release archives contain `soundboard`, `README.md`, `docs/DEPLOYMENT.md`, `compose.yml`, `compose.dev.yml` and an example metadata file. Install `ffprobe` for standalone binary deployments.

For manual builds, pass `-ldflags="-X main.buildVersion=v0.1.1"` to `go build`; without it `soundboard -version` prints `dev`. The Dockerfile accepts `--build-arg VERSION=v0.1.1` for the same purpose.

## Reverse proxy and authorization

The app intentionally has no login system. Protect all soundboard traffic via Caddy/Authelia or another trusted reverse proxy, and apply a stronger administrative policy to the following URL paths:

| Route | Access policy |
| --- | --- |
| `/` (and `/style.css`, `/app.js`, `/audio.js`, `/favicon.ico`, `/icon-*.png`, `/apple-touch-icon.png`) | Soundboard audience |
| `/ws` | Soundboard audience; WebSocket upgrade |
| `/api/sounds`, `/api/audio/*` | Soundboard audience |
| `/admin/`, `/admin/admin.js`, `/admin/admin.css` | Administrators only |
| `/admin/ws` | Administrators only; WebSocket upgrade |
| `/api/admin/*` | Administrators only; includes write/delete APIs |
| `/healthz` | Restricted to local health checks if desired |

Use path-specific rules at the proxy; guarding only the Admin HTML page does not guard the API. The app trusts nicknames supplied by browsers for display, not for authorization. Restrict access to the container's port so nobody can bypass the proxy. WebSocket Upgrade requests must be forwarded for `/ws` and `/admin/ws`.

The dedicated Admin WebSocket registers which browser identities are administering. While any admin tab for a browser identity remains connected, regular playback in that browser's other player tabs is suppressed. Closing the final admin tab returns the listener to normal; it does not replay an already-started sound.

## Updates, backups and data refresh

Back up the full `sounds/` directory (including optional `metadata.json` and `groups.json`). Do not copy an empty `sounds/` directory over your existing bind mount when updating code. Changes made through Admin actions are visible immediately. Changes made directly on disk require **Resync** in `/admin/` (or a server restart); there is no periodic filesystem scan. Queue and participant data are ephemeral and disappear on restart.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Upload/delete fails | Host `sounds/` ownership, `PUID`/`PGID`, writable bind mount |
| Admin page works but writes fail | Access rule for `/api/admin/*` and browser network response |
| Admin status never appears | WebSocket authorization/upgrade for `/admin/ws` |
| Participants do not update | WebSocket upgrade for `/ws` |
| Sounds do not play | Browser audio activation, file format support, HTTP response for `/api/audio/<filename>` (HTTP 206 for range requests is normal) |
| Sound added on disk not visible | Manually click **Resync** in Admin |
| App works locally but not through proxy | Ensure all routes use the same origin and that WebSockets and audio requests are forwarded |

The application does not perform format conversion during playback. The Admin preview uses the same original audio endpoint as the player; a browser may not support every provided codec or container. Some mobile browsers block background-tab audio.
