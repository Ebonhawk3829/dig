# dig

Album-first music browser for [slskd](https://github.com/slskd/slskd).

dig does one thing: find an album on Soulseek and fetch it. It is a companion
tool, not an all-in-one suite — pair it with whatever tagger (wrtag, beets,
Picard) and library server (Navidrome, Jellyfin, …) you already run.

## Usage

1. Open dig, type `artist - album` (e.g. `Toto - Toto IV`), hit **Dig**.
2. Candidates are scored and colour-coded: green (accept), yellow (review),
   red (rejected — e.g. live/bootleg/box-set markers).
3. Click **Download This**. dig blocks duplicate enqueues from the same source.
4. Watch progress on the Transfer Status page. When a download shows
   **Succeeded**, it has been handed off to your tagger.

## Configuration

Environment variables (all optional):

| Variable | Default | Purpose |
|---|---|---|
| `DIG_SLSKD_URL` | `http://slskd:5030` | slskd base URL |
| `DIG_SLSKD_API_KEY` | — | slskd API key (required) |
| `DIG_SLSKD_SEARCH_TIMEOUT_MS` | `15000` | per-rung search timeout |
| `DIG_SERVER_PORT` | `8080` | listen port |
| `DIG_SERVER_AUTH_USERNAME` / `_PASSWORD` | — | optional basic auth |
| `DIG_LIBRARY_SUBSONIC_URL` / `_USER` / `_PASSWORD` | — | optional library check (Navidrome etc.) |
| `DIG_NOTIFY_URL` | — | optional webhook for download notifications |
| `DIG_CONFIG` | — | path to a full `dig.yaml` |

### MusicBrainz rate limiting

MusicBrainz limits requests per IP. dig enforces its own 1 req/s cap and
automatically retries rate-limited requests (up to ~20 seconds of backoff), so
occasionally a search just takes a little longer instead of failing. If
MusicBrainz is unreachable entirely, dig falls back to manual artist + album
search so downloads keep working.

Most of these can also be set in the **Settings page** (`/settings`) after
startup — values saved there persist in the database, apply immediately
without a restart, and override env/YAML. Each section has a **Test
connection** button; secrets are write-only (blank means "unchanged").

## Widgets & monitoring

- `GET /healthz` — liveness + per-dependency status (slskd / MusicBrainz / library)
- `GET /api/status` — compact summary for dashboards/widgets: overall status,
  active/failed download counts, aggregate transfer speed, files completed today
- `GET /api/transfers` — live per-download detail (the transfers page polls this)

## Theming

Dark mode by default; the ☀/☾ toggle in the header switches to light and is
remembered per browser (localStorage).

## Running

```sh
docker compose up -d
```

dig must be able to reach slskd over HTTP. In the provided compose file both
services share a network so `http://slskd:5030` resolves by service name; if
slskd runs elsewhere, point `DIG_SLSKD_URL` at its reachable address.
