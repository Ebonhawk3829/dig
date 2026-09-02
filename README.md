<div align="center">

<img src="internal/web/static/img/logo.png" width="110" alt="dig logo">

# dig

**Find an album. Get it. That's it.**

An album-first music browser for [slskd](https://github.com/slskd/slskd): one click flows
through slskd, your tagger, and into your music library.

[![Release](https://img.shields.io/github/v/release/Ebonhawk3829/dig?style=flat-square)](https://github.com/Ebonhawk3829/dig/releases)
[![Docker Image](https://img.shields.io/badge/ghcr.io-ebonhawk3829%2Fdig-blue?style=flat-square&logo=docker)](https://ghcr.io/Ebonhawk3829/dig)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/Ebonhawk3829/dig/ci.yml?label=CI&style=flat-square)](https://github.com/Ebonhawk3829/dig/actions)

</div>

---

**dig** is a small web app for people who run a self-hosted music library. You
tell it *"I want this album"*, and it finds the album on Soulseek, shows you the
best available copies, and downloads the one you pick. From there your existing
tools take over: your tagger cleans it up, your music server picks it up, and
it lands in your library ready to play.

dig is a **companion tool, not an all-in-one suite**. It doesn't tag, rename,
organise, or play music; it pairs with the tools you already run:

| Role | Required? | Common choices |
|---|---|---|
| Soulseek client | **yes** | [slskd](https://github.com/slskd/slskd) |
| Tagger / importer | optional | wrtag, beets, Picard (manual) |
| Music library server | optional | Navidrome, Jellyfin, Airsonic, Plex |

## Installation

> **Before you start:** you need a running [slskd](https://github.com/slskd/slskd)
> instance and its API key (slskd > Settings > API Keys). Everything else is optional.

### Docker Compose (recommended)

The simplest way to run dig is alongside slskd in a compose file:

```yaml
services:
  slskd:
    image: slskd/slskd:latest
    ports:
      - "5030:5030"        # web UI + API
    # ... your existing slskd config ...

  dig:
    image: ghcr.io/ebonhawk3829/dig:latest
    ports:
      - "8080:8080"        # dig's web UI
    environment:
      - DIG_SLSKD_URL=http://slskd:5030
      - DIG_SLSKD_API_KEY=your-api-key-here
      - DIG_DB_PATH=/data/dig.db
    volumes:
      - dig-data:/data
    restart: unless-stopped

volumes:
  dig-data:
```

Then:

```sh
docker compose up -d
```

Open **http://localhost:8080** and you're running.

> **Tip:** both services are on the same compose network, so dig reaches slskd
> at `http://slskd:5030` by service name. If slskd runs on a different machine,
> use its address instead, e.g. `http://192.168.1.50:5030`.

### Docker run

No compose? One container is all it takes:

```sh
docker run -d --name dig \
  -p 8080:8080 \
  -e DIG_SLSKD_URL=http://your-slskd-host:5030 \
  -e DIG_SLSKD_API_KEY=your-api-key-here \
  -v dig-data:/data \
  --restart unless-stopped \
  ghcr.io/ebonhawk3829/dig:latest
```

### From source

Requires [Go](https://go.dev/) 1.25 or newer:

```sh
git clone https://github.com/ebonhawk3829/dig
cd dig
go build ./cmd/dig
DIG_SLSKD_URL=http://localhost:5030 DIG_SLSKD_API_KEY=your-key ./dig
```

## Configuration

**You can configure almost everything from dig's own Settings page**, no file
editing required. Open the app, go to Settings, fill in what you use, and hit
**Test connection** to check it before saving. Changes apply instantly.

| Setting | What it does |
|---|---|
| **slskd** | URL and API key of your slskd instance (required) |
| **Music metadata** | MusicBrainz connection, provides search, discographies and tracklists |
| **Library check** | Optional. Points at a Subsonic-compatible server (Navidrome, Airsonic, ...) so dig can warn you when you already have an album |
| **Notifications** | Optional webhook: dig sends a message when a download completes, fails, or finishes partially. Works with ntfy, Gotify, or any webhook endpoint |

<details>
<summary><strong>Environment variables</strong> (for automation-friendly setups)</summary>

| Variable | Default | Purpose |
|---|---|---|
| `DIG_SLSKD_URL` | `http://slskd:5030` | slskd base URL |
| `DIG_SLSKD_API_KEY` | — | slskd API key (required) |
| `DIG_SLSKD_SEARCH_TIMEOUT_MS` | `15000` | per-query search timeout |
| `DIG_SERVER_PORT` | `8080` | listen port |
| `DIG_SERVER_AUTH_USERNAME` / `_PASSWORD` | — | optional basic auth |
| `DIG_LIBRARY_SUBSONIC_URL` / `_USER` / `_PASSWORD` | — | optional library check |
| `DIG_NOTIFY_URL` | — | optional webhook for notifications |
| `DIG_MUSICBRAINZ_USER_AGENT` | `dig/0.1 (dig@example.com)` | MusicBrainz asks clients to identify themselves |
| `DIG_MUSICBRAINZ_BASE_URL` | `https://musicbrainz.org/ws/2` | point at a mirror if the main instance blocks your host |
| `DIG_DB_PATH` | `/data/dig.db` | SQLite database path |

Settings entered in the UI override these values.

</details>

<details>
<summary><strong>How dig handles MusicBrainz rate limits</strong></summary>

MusicBrainz limits requests per IP. dig enforces its own 1 request/second cap
and automatically retries rate-limited requests (up to ~20 seconds of backoff),
so occasionally a search just takes a little longer instead of failing. If
MusicBrainz is unreachable entirely, dig falls back to manual artist + album
search so downloads keep working.

</details>

## Using dig

The workflow is one loop: **search, pick, download, done.**

### 1. Search

Type an artist, an album, or both, and hit **Dig**. Results appear as artist
links and album cards with cover art. Albums already in your library are
badged **IN LIBRARY** so you don't re-download things you own.

### 2. Browse

Click an artist to see their discography as a cover-art grid; use the filter
pills (Studio / Albums / EPs / Singles / Other / All) to narrow it down. Click
an album to see its tracklist, cover art, and available editions.

### 3. Dig

Hit **Find on Soulseek** on an album page. dig searches Soulseek using a ladder
of queries (most specific first, with wildcarded fallbacks for artists whose
names get blocked), groups the results into candidate folders, and scores each
one:

- **accept**: good match, complete, healthy peer
- **review**: worth a look, something's off (partial, mixed formats...)
- **rejected**: wrong product (live/bootleg/box set) or poor match

Expand a card to see the exact files before you commit.

### 4. Download

Click **Download This** on the candidate you want. dig refuses to enqueue the
same folder twice, so you can't accidentally double-download.

### 5. Watch it land

The **Transfers** page updates live: byte-accurate progress bars and transfer
speeds, refreshing every 3 seconds. If a download fails, you get **Retry** and
**Clear** buttons right on the card.

When a download shows **completed**, the files are in slskd's download
folder and dig's job is done; your tagger/importer picks them up from there
and they appear in your music library like any other album.

## For dashboards and widgets

dig exposes a few read-only JSON endpoints for Homepage, OliveTin, or anything
else that speaks HTTP:

| Endpoint | What it gives you |
|---|---|
| `GET /healthz` | Liveness + per-dependency status (slskd / MusicBrainz / library) |
| `GET /api/status` | Compact summary: overall status, active/failed download counts, aggregate transfer speed, files completed today |
| `GET /api/transfers` | Full live per-download detail |

## Theming

Dark mode by default; the sun/moon toggle in the header switches to light and
is remembered per browser.

## FAQ

<details>
<summary><strong>Does dig download things automatically?</strong></summary>

No. dig shows you ranked candidates and waits for you to choose. The only
automation is the download you confirm.

</details>

<details>
<summary><strong>Does dig tag or rename my files?</strong></summary>

No, that's the job of your tagger (wrtag, beets, Picard). dig's contract ends
when the files land in slskd's download folder.

</details>

<details>
<summary><strong>Does dig need to run on the same machine as slskd?</strong></summary>

No. dig just needs HTTP access to slskd's API. Same machine, same Docker
network, or a different host entirely, all fine.

</details>

<details>
<summary><strong>Why does my first search sometimes take ~20 seconds?</strong></summary>

MusicBrainz rate-limits by IP. dig waits briefly and retries automatically
rather than failing. Later searches are fast because results are cached.

</details>

## License

[MIT](LICENSE)
