# Soundboard

Initial release: **v0.1.0** · [GitHub repository](https://github.com/desirevolution/soundboard)

A small, self-hosted, multi-user soundboard. Sounds play in each connected browser; the Go server coordinates a shared FIFO queue and a separate global instant-play channel over WebSockets. There is no central audio stream, user account system, database, or audio preloading.

## Features

- **Click / tap:** play immediately for everyone. The next instant sound replaces the previous one globally, including a repeated click on the same sound.
- **Shift + click / long press:** enqueue on the global FIFO channel (maximum **20 waiting** sounds); this channel runs independently of instant playback.
- Local search, group filter and sorting (A–Z, Z–A, group then name); each browser keeps its own preferences. Sounds remain audible even when filtered out.
- Fixed-size round buttons with a tactile 3D press effect and automatic light/dark appearance. Group colors are shared and persisted.
- Live participant list with nicknames, colors, active player indicators and waiting counts. Nicknames are stored locally and are **not authenticated identities**.
- Compact bottom playback panel: only active channels are shown; a waiting-only queue stays visible. Brief action messages are separate.
- Admin mode silences global sounds in other player tabs sharing the same browser identity, but allows local preview. Other participants see the Admin status.
- Administration: upload one or multiple files, import direct audio URLs, rename on conflicts or replace while retaining metadata, edit names/groups/colors, preview locally, delete sounds and manually resync the library.

All browser audio is loaded on demand through the original audio file endpoint. Start times may differ across devices. Some browsers require an explicit interaction to enable sound.

## Run with Docker Compose

**Normal deployment** uses the published multi-platform image `ghcr.io/desirevolution/soundboard:latest` and listens on **all host interfaces** (`0.0.0.0:8080`). Publish GitHub Release `v0.1.0` first so the image exists, or select a published image using `SOUNDBOARD_IMAGE`.

```bash
mkdir -p sounds
PUID=$(id -u) PGID=$(id -g) docker compose up -d
```

Soundboard: `http://HOST:8080/` · Administration: `http://HOST:8080/admin/`.

**Development build** uses `compose.dev.yml`: it builds the source locally, and binds **only `127.0.0.1`** by default:

```bash
PUID=$(id -u) PGID=$(id -g) docker compose -f compose.dev.yml up -d --build
```

The normal Compose file allows remote access to port 8080. **The app has no built-in authentication:** place it behind authenticated Caddy/Authelia (including Admin and WebSocket/API routes), and do not allow unauthenticated traffic to reach the published port. You can set `SOUNDBOARD_BIND=127.0.0.1` if using a local reverse proxy, or avoid a host port entirely if proxying over a shared Docker network. Set `SOUNDBOARD_PORT` to change the host port.

The bind-mounted `./sounds` directory must be writable by `PUID:PGID` for uploads and deletes, and must be preserved during upgrades. See [Deployment](docs/DEPLOYMENT.md).

**Run without Docker:** requires Go 1.23+ and `ffprobe`:

```bash
go run . -sounds ./sounds -addr :8080
```

Check the installed binary version with `soundboard -version` (initially `v0.1.0`).

## Audio and data

Put audio files in one flat directory. The optional metadata files live alongside them:

```text
sounds/
  applause.mp3
  bell.wav
  metadata.json
  groups.json
```

**`metadata.json`** maps an exact filename to an optional display name and group:

```json
{
  "applause.mp3": {
    "name": "Applause",
    "group": "reactions",
    "uploadedBy": "Alex",
    "uploadedAt": "2026-10-29T18:30:00Z"
  }
}
```

Missing entries use the filename without extension as the display name and the reserved group `default`. The Admin UI records the display-only uploader nickname and upload time for new files, and preserves original attribution when replacing an existing sound.

**`groups.json`** stores a group name to color mapping, for example:

```json
{
  "default": "#8492a9",
  "reactions": "#db5359"
}
```

The `default` group always exists and cannot be deleted or renamed. New groups receive a randomly proposed, reasonably distinct color that is saved permanently and can be edited later. Metadata and group edits are written atomically.

Supported extensions are `.mp3`, `.wav`, `.ogg`, `.opus` and `.m4a`. Admin uploads are limited to 20 MB per file, 30 seconds per sound, 20 files and 105 MB per request. URL imports must be direct public HTTP(S) audio URLs; internal addresses and redirects are rejected. The runtime image includes `ffprobe` for audio validation and duration detection.

**No file polling:** The library is loaded on startup and updated after Admin changes. If you modify files manually, use **Resync** in `/admin/`. Online browsers are updated via WebSocket.

## GitHub Actions and releases

The workflow in `.github/workflows/build-and-release.yml`:

| Event | Checks | GHCR images | Release files |
| --- | --- | --- | --- |
| Pull request to `main` | Go tests (race detector), vet | None | None |
| Push to `main` | Same | `main`, `sha-…` (`amd64`/`arm64`) | None |
| GitHub Release **published** with a semantic version tag, e.g. `v0.1.0` | Same | `0.1.0`, `0.1`, `latest` (`amd64`/`arm64`) | Linux `amd64`/`arm64` tarballs and SHA256SUMS |

The workflow checks that the release tag matches the `VERSION` file and publishes to `ghcr.io/desirevolution/soundboard` using the repository's `GITHUB_TOKEN`. Grant Actions **read/write package permissions** if your repository or organization restricts them. Publish a GitHub Release to publish `latest`; pushes to `main` do **not** update `latest`.

Release archives contain the Go binary and basic project documentation. `ffprobe` must be installed separately if using these binaries directly. See [Deployment](docs/DEPLOYMENT.md) for Compose, GHCR, external access control, and troubleshooting.

## Access control

**Soundboard has no built-in authentication.** The reverse proxy must protect the entire application appropriately. For administrators, restrict **all three** paths: `/admin/*`, `/admin/ws`, and `/api/admin/*`; the admin API can modify and delete files. The gear icon is only a link, not access control. Never expose the service port publicly without a trusted reverse proxy and authorization.

See [Deployment and security](docs/DEPLOYMENT.md) for proxy requirements and the security implications of client-provided nicknames.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

Tests cover the server, queue, groups, import/resync flows, media endpoints and regression scenarios. State (queue, presence, instant playback) is held in memory; restarting the single server instance clears active playback and waiting entries, but does not delete audio or metadata.

## Current limitations

- Only **one server instance** is supported: shared queue and presence are not coordinated across replicas.
- Browser autoplay restrictions, device audio latency and network conditions affect when sound starts; exact sync is not guaranteed.
- Admin mode is based on browser-tab presence; nicknames and uploader attribution are for display only and must not be treated as audit-grade identities.
