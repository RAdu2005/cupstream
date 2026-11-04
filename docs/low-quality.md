# Low-quality versions

A stream can have a low-quality version for viewers on slow connections: roughly 480p30 to 720p30 at 1–1.5 Mbit/s, H.264 without B-frames, with the original's Opus audio (overview, 7.3). It comes from one of two sources:

- **Sent:** the desktop app publishes it as a second WHIP session.
- **Transcoded:** for streams without one (OBS, browsers, or the app with the setting off), MediaMTX runs FFmpeg while someone watches it, if the server allows transcodes.

## Configuration

`CUPSTREAM_LOW_QUALITY=on|off` (default `on`): whether the server accepts low-quality versions from senders and offers them, or transcodes, to viewers. With `off`, `POST /api/streams` answers `lowQuality.publish: false`, a sender's low-quality WHIP request is refused with 403 `low_quality_off`, and every stream reports `lowQuality: "none"`. Changing it needs a restart.

`CUPSTREAM_TRANSCODES=<n>` (default `0`, none): how many transcodes may run at once. Each needs FFmpeg on the MediaMTX host and the transcode path in `mediamtx.yml` (see [Transcodes](#transcodes)), so keep it at 0 until both are in place. Admins can change the limit on `/admin` (`PUT /api/admin/transcodes`, `{"max": n}`) until the server restarts, when `CUPSTREAM_TRANSCODES` applies again; lowering it stops none of the running transcodes.

## Senders

`POST /api/streams` answers `lowQuality: {"publish": bool}`. When `publish` is true, the sender may publish the low-quality version with WHIP at its `whipUrl` plus `?quality=low` (`POST /api/streams/{id}/whip?quality=low`), with the same token. Its session URL (`/api/media/{id}/low/whip/{secret}`) takes trickle-ICE `PATCH` and `DELETE` like the stream's own.

- It needs the stream's own publisher: before that, it is refused with 409 `no_main_session`.
- The stream ending ends it.
- It can end alone (`DELETE`, or its connection dropping); the stream goes on, and the sender can publish it again.
- A reconnect replaces only the session of the same version.

In MediaMTX, the low-quality version has the path `s/<streamId>/low`, next to the stream's `s/<streamId>`; `all_others` covers both, so `mediamtx.yml` needs no change.

## Transcodes

A viewer who asks for the low-quality version of a stream without a sent one reads the stream's transcode path, `s/<streamId>/transcode`. Its `runOnDemand` command in [mediamtx.yml](../deploy/mediamtx/mediamtx.yml) starts FFmpeg, which reads the stream from MediaMTX over RTSP (on `127.0.0.1` only, TCP) and publishes it back over RTSP:

- video at most 720p and 30 fps, H.264 Constrained Baseline without B-frames, x264 `veryfast`/`zerolatency`, 1.2 Mbit/s, a keyframe every second;
- the stream's Opus audio, unchanged;
- 2 threads for decoding and 2 for encoding, which bounds what one transcode can take.

The viewer's WHEP request waits until FFmpeg publishes, about 1–2 seconds; MediaMTX gives up after 8. MediaMTX stops FFmpeg 10 seconds after its last reader left, and kills it if it has not stopped 10 seconds after that (fork change 15). It restarts FFmpeg if it exits while viewers remain. A transcode adds latency (the overview estimates 200–500 ms; not measured yet), and FFmpeg cannot answer keyframe requests, so a new or recovering viewer waits for the next keyframe, at most a second.

The server counts a transcode as running while MediaMTX reports its path online, while it has viewers, and for 30 seconds after a viewer was admitted to it, so two viewers cannot take the last place at once. A stream offers `lowQuality: "transcoded"` while it is live and either its transcode runs or fewer than the limit do. Once the limit is reached, the other streams offer `"none"` and a viewer asking for one is refused with 409 `low_quality_unavailable`; the player then plays the source. A viewer of a transcode who receives nothing (paused, or held after losing access) for 30 seconds is disconnected, because their session alone keeps FFmpeg running; the player closes a transcode's session when its stream is paused anyway. If FFmpeg does not start (not installed, say), the WHEP request fails after 8 seconds, the server logs a warning, and the player plays the source.

Cost, from the load test (Ryzen 9 6900HS, 8 cores and 16 threads, 1080p60 sources at 8 Mbit/s): about 0.5 cores per transcode of a screen-like picture and 0.65 cores of a noisy one, with short peaks of up to one core while FFmpeg starts. 12 noisy transcodes at once (10.5 cores) still kept up. See [server/loadtest](../server/loadtest/README.md#transcodes-4-november-2026).

## Viewers

The `Stream` type's `lowQuality` is `"none"`, `"sent"` or `"transcoded"`: what a viewer gets if they pick the low-quality version; a sent one takes precedence over a transcode. Realtime `stream` events carry it when it changes, and `lowBitrate` is what the server receives of a sent one. Viewers open it with `POST /api/streams/{id}/whep?quality=low`, with the same access rules, `?paused=true&preview=false` and `?tab=` as the stream itself; without one, the request is refused with 409 `low_quality_unavailable`. Its session URL is `/api/media/{id}/low/whep/{secret}`, or `/api/media/{id}/transcode/whep/{secret}` for a transcode, with `.../pause` and `.../resume` as usual.

Viewers of the low-quality version count as viewers of the stream: in its viewer count, towards the 4 streams a viewer may receive at once, and in admission control, where they reserve the low-quality version's measured bitrate (1.5 Mbit/s until it is known, and for transcodes). The sender's bitrate target leaves them out, since they receive another encoding.

## The player

Each watched stream with a low-quality version has a quality picker: **Auto**, **Source** or **Low**. The browser remembers the last choice (`localStorage`, `cupstream.quality`), and streams start with it; Auto is the default.

- **Auto** plays the source and switches to the low-quality version while the viewer's connection is the bottleneck: when every playing stream gets worse together (the bandwidth warning, overview 7.2), or when a single playing stream struggles while the server receives it from its sender without losses. It tries the source again after a minute, then after twice as long each time the source struggles again, up to 15 minutes.
- The bandwidth warning offers **Switch to low quality** for the playing streams that have one.
- Switching opens the other version's session and swaps it in once its video arrives, so the picture goes on meanwhile. While the low-quality version plays, the source's session stays open but paused (like a pre-warmed stream), so switching back only waits for a key frame. A transcoded version costs the server while its session is open, so a paused stream goes back to its paused source session.
- If the low-quality version goes away or is refused, the stream plays its source.

## Trying it without the desktop app

The load test publishes low-quality versions too: `-low-bitrate 1000 -low-size 854x480` adds one to each synthetic stream, and `-low-viewers 0.5` sends half the viewers to it (see [server/loadtest](../server/loadtest/README.md)). Without `-low-bitrate`, those viewers get transcodes: run `make dev` with `CUPSTREAM_TRANSCODES` above 0 (the development MediaMTX image includes FFmpeg).
