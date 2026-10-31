# MediaMTX build

MediaMTX runs from the fork [RAdu2005/mediamtx](https://github.com/RAdu2005/mediamtx): an upstream release plus the WHEP pause/resume pull request [bluenviron/mediamtx#6308](https://github.com/bluenviron/mediamtx/pull/6308) and our changes (overview, section 3.2). The tag is pinned in `deploy/mediamtx/Dockerfile` and `compose.yaml`; the fork's `CUPSTREAM.md` describes how to upgrade.

The server reads the build's version from MediaMTX at startup. From `v1.21.2-cupstream.2` on, it follows MediaMTX's event stream (`/v3/events`) and pauses viewers through the API; with any other build it falls back to polling every 2 seconds and to closing sessions instead of pausing them. `/healthz` reports which is in use.

Viewers pause and resume their own sessions with `POST /api/media/{stream}/whep/{secret}/pause` and `.../resume` (optional body `{"video": bool, "audio": bool}`), and open pre-warmed sessions with `POST /api/streams/{id}/whep?paused=true&preview=false`. A room holds up to 8 streams and a viewer receives up to 4 at once (a signed-in user across their tabs; paused sessions do not count); beyond that the server answers 409 with `room_full` or `too_many_streams`. A viewer who loses access is held: their media is paused, they cannot resume it, it comes back if they regain access, and the session is closed after 2 minutes.

On the host, raise the UDP send buffer limit to match MediaMTX's `udpWriteBufferSize` (8 MiB), or Linux silently caps it:

```sh
echo 'net.core.wmem_max = 8388608' | sudo tee /etc/sysctl.d/90-cupstream.conf
sudo sysctl --system
```
