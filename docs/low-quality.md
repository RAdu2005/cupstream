# Low-quality versions

A stream can have a low-quality version for viewers on slow connections: roughly 480p30 to 720p30 at 1–1.5 Mbit/s, H.264 without B-frames, with the original's Opus audio (overview, 7.3). For now it comes from the desktop app, which publishes it as a second WHIP session. The server transcoding one on demand for OBS and browser streams comes in a later change.

## Configuration

`CUPSTREAM_LOW_QUALITY=on|off` (default `on`): whether the server accepts low-quality versions from senders and offers them to viewers. With `off`, `POST /api/streams` answers `lowQuality.publish: false`, a sender's low-quality WHIP request is refused with 403 `low_quality_off`, and every stream reports `lowQuality: "none"`. Changing it needs a restart.

## Senders

`POST /api/streams` answers `lowQuality: {"publish": bool}`. When `publish` is true, the sender may publish the low-quality version with WHIP at its `whipUrl` plus `?quality=low` (`POST /api/streams/{id}/whip?quality=low`), with the same token. Its session URL (`/api/media/{id}/low/whip/{secret}`) takes trickle-ICE `PATCH` and `DELETE` like the stream's own.

- It needs the stream's own publisher: before that, it is refused with 409 `no_main_session`.
- The stream ending ends it.
- It can end alone (`DELETE`, or its connection dropping); the stream goes on, and the sender can publish it again.
- A reconnect replaces only the session of the same version.

In MediaMTX, the low-quality version has the path `s/<streamId>/low`, next to the stream's `s/<streamId>`; `all_others` covers both, so `mediamtx.yml` needs no change.

## Viewers

The `Stream` type's `lowQuality` is `"none"`, `"sent"` or `"transcoded"`: what a viewer gets if they pick the low-quality version. Realtime `stream` events carry it when it changes, and `lowBitrate` is what the server receives of a sent one. Viewers open it with `POST /api/streams/{id}/whep?quality=low`, with the same access rules, `?paused=true&preview=false` and `?tab=` as the stream itself; without one, the request is refused with 409 `low_quality_unavailable`. Its session URL is `/api/media/{id}/low/whep/{secret}`, with `.../pause` and `.../resume` as usual.

Viewers of the low-quality version count as viewers of the stream: in its viewer count, towards the 4 streams a viewer may receive at once, and in admission control, where they reserve the low-quality version's measured bitrate (1.5 Mbit/s until it is known). The sender's bitrate target leaves them out, since they receive another encoding.

## The player

Each watched stream with a low-quality version has a quality picker: **Auto**, **Source** or **Low**. The browser remembers the last choice (`localStorage`, `cupstream.quality`), and streams start with it; Auto is the default.

- **Auto** plays the source and switches to the low-quality version while the viewer's connection is the bottleneck: when every playing stream gets worse together (the bandwidth warning, overview 7.2), or when a single playing stream struggles while the server receives it from its sender without losses. It tries the source again after a minute, then after twice as long each time the source struggles again, up to 15 minutes.
- The bandwidth warning offers **Switch to low quality** for the playing streams that have one.
- Switching opens the other version's session and swaps it in once its video arrives, so the picture goes on meanwhile. While the low-quality version plays, the source's session stays open but paused (like a pre-warmed stream), so switching back only waits for a key frame. A transcoded version costs the server while its session is open, so a paused stream goes back to its paused source session.
- If the low-quality version goes away or is refused, the stream plays its source.

## Trying it without the desktop app

The load test publishes low-quality versions too: `-low-bitrate 1000 -low-size 854x480` adds one to each synthetic stream, and `-low-viewers 0.5` sends half the viewers to it (see [server/loadtest](../server/loadtest/README.md)).
