# MediaMTX build

MediaMTX runs from the fork [RAdu2005/mediamtx](https://github.com/RAdu2005/mediamtx): an upstream release plus the WHEP pause/resume pull request [bluenviron/mediamtx#6308](https://github.com/bluenviron/mediamtx/pull/6308) and our changes (overview, section 3.2). The tag is pinned in `deploy/mediamtx/Dockerfile` and `compose.yaml`; the fork's `CUPSTREAM.md` describes how to upgrade.

Production runs `v1.21.2-cupstream.4`. It lets several UDP sockets share port 8189 (`webrtcLocalUDPSockets: 8` in [mediamtx.yml](../deploy/mediamtx/mediamtx.yml)), so that sessions send in parallel. With one socket, MediaMTX's output stopped at about 970 Mbit/s in the load test (see [server/loadtest](../server/loadtest/README.md)). Builds before `.4` refuse that setting. The server warns at startup, and whenever MediaMTX's version changes, if the build is older than the pinned one (`mediamtx.PinnedBuild`, which a test keeps equal to the image pins).

The server reads the build's version from MediaMTX at startup, and again whenever MediaMTX restarts. From `v1.21.2-cupstream.2` on, it follows MediaMTX's event stream (`/v3/events`) and pauses viewers through the API; with any other build it falls back to polling every 2 seconds and to closing sessions instead of pausing them. `/healthz` reports which is in use.

Viewers pause and resume their own sessions with `POST /api/media/{stream}/whep/{secret}/pause` and `.../resume` (optional body `{"video": bool, "audio": bool}`), and open pre-warmed sessions with `POST /api/streams/{id}/whep?paused=true&preview=false`. A room holds up to 8 streams and a viewer receives up to 4 at once (a signed-in user across their tabs; paused sessions do not count); beyond that the server answers 409 with `room_full` or `too_many_streams`. A viewer who loses access is held: their media is paused, they cannot resume it, it comes back if they regain access, and the session is closed after 2 minutes.

On the host, raise the UDP send buffer limit to match MediaMTX's `udpWriteBufferSize` (8 MiB), or Linux silently caps it:

```sh
echo 'net.core.wmem_max = 8388608' | sudo tee /etc/sysctl.d/90-cupstream.conf
sudo sysctl --system
```

## Upgrading in production

MediaMTX is installed by hand (see [Deployment](../README.md#deployment)).

**MediaMTX applies every change to its configuration file at once.** It reloads the file as soon as it changes. A setting the running build does not know, or any other invalid edit, makes it log the error and exit. That exit is clean, so `Restart=on-failure` does not start it again: on 31 October 2026, adding `webrtcLocalUDPSockets` while `.3` was still running stopped MediaMTX until it was started by hand. So stop MediaMTX before changing its configuration together with its binary.

To move to a new pinned tag:

1. Take the binary from the image as for installing, check that `./mediamtx --version` prints the tag, and note its `sha256sum`.
2. Copy it to the server, and check the checksum there.
3. Keep the old binary and `/etc/mediamtx/mediamtx.yml` next to the new ones, e.g. as `/usr/local/bin/mediamtx.<old build>` and `/etc/mediamtx/mediamtx.yml.<old build>`.
4. Check that nobody is live, with `/admin` or MediaMTX's API: `curl -s 'http://127.0.0.1:9997/v3/webrtc/sessions/list?itemsPerPage=1000' | jq '.items | length'` should print 0. Stopping ends every live stream and viewer session.
5. `sudo systemctl stop mediamtx`.
6. Apply the release's configuration changes (the pull request that bumps the pin lists them), and install the new binary.
7. `sudo systemctl start mediamtx`, then check `journalctl -u mediamtx` for the new version and no `ERR`. The CupStream server notices the restart by itself and reads the new version.
8. Check that `/healthz` reports the new version under `mediamtx`.

To roll back, stop `mediamtx`, put both the old binary and the old configuration back (an older build may refuse newer settings), then start it.
