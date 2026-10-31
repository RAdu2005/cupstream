# Load test

Synthetic WHIP publishers and WHEP viewers, to find where a development server saturates (overview, section 10). The harness is a module of its own, so pion stays out of the server's dependencies.

**Only against `make dev` on this machine.** The harness refuses any server that is not on localhost. Never point it at production. It needs ffmpeg, which encodes the test clip once into the temporary directory.

```sh
# Another worktree's copy uses other ports; rate limits are per address, and
# every synthetic viewer comes from 127.0.0.1.
DEV_WEB_PORT=5177 DEV_SERVER_PORT=8084 CUPSTREAM_RATE_LIMITS=false CUPSTREAM_EGRESS_LIMIT_MBPS=0 make dev

cd server/loadtest
go run . -server http://127.0.0.1:8084 -mediamtx-api http://127.0.0.1:10001 \
  -mediamtx-pid "$(docker inspect -f '{{.State.Pid}}' cupstream-8084-mediamtx-1)" \
  -streams 1 -bitrate 8000 -viewers 150 -ramp 10 -hold 10s
```

The MediaMTX API port is 9997 plus the dev server's port offset (8084: 10001).

What it does:

- **Publishers** (`-streams`) sign in with the development login as `loadtest_pub_<n>`, open their room to everyone, start a web stream and send a ten-second clip of moving noise over WHIP in a loop. The clip is H.264 Constrained Baseline with no B-frames and a keyframe every 2 s, at `-bitrate`, `-size` and `-fps`. Like a browser, a publisher answers keyframe requests by jumping to the next keyframe. `-watch <id,...>` watches existing streams instead, e.g. from OBS.
- **Viewers** (`-viewers`, opened at `-ramp` per second and spread over the streams) open WHEP sessions signed out, like visitors. They don't decode: they count bytes, lost packets (sequence gaps) and the time to the first keyframe. With `-prewarm`, they open paused without a preview frame and resume a second later. Each viewer's UDP socket gets a 4 MiB receive buffer (`-rcvbuf`, capped by `net.core.rmem_max`). With the default 208 KiB, keyframes that reach every viewer at once overflow the harness's own sockets.
- **Every second**, it prints:
  - viewers playing, starting, refused (503, admission control), rate-limited (429) and failed
  - what they received, the loss, and how many are *starved* (under 80% of the bitrate)
  - the host's UDP receive-buffer drops (`RcvbufErrors`); non-zero means the harness itself can't keep up
  - what MediaMTX reports sending
  - the CPU of MediaMTX (`-mediamtx-pid`), the server (`-server-pid`) and the harness
- **At the end**, it prints a summary and deletes every session and stream.

Keep runs short when other people share the machine: 150 viewers of 8 Mbit/s move about 1.2 Gbit/s over loopback and take a few cores.

## Results (30 October 2026, development laptop)

16 threads, 22 GB, other work running. MediaMTX `v1.21.2-cupstream.3` in Docker with host networking, everything over loopback.

| Run | Playing | Received | Loss | Starved | MediaMTX CPU |
|---|---|---|---|---|---|
| 1 stream × 150 viewers, 8.2 Mbit/s 1080p60 | 149 | 780–870 Mbit/s plateau | 0% | most, from ~105 viewers | 1.3–1.7 cores |
| 3 streams × 50 viewers, 8.2 Mbit/s | 150 | 800–900 Mbit/s plateau | 0% | most, from ~105 viewers | 1.3–1.8 cores |

- **Saturation at about 850–900 Mbit/s out of MediaMTX, around 105 viewers of an 8 Mbit/s stream, whatever the number of streams.** Above that, viewers get less than the bitrate with no packet loss: MediaMTX drops whole frames for readers it can't keep up with, before packetizing them. This happens before 1 Gbit/s and well before the CPU is busy. MediaMTX uses under 2 of 16 cores with no thread pegged, which points at its shared send path (all viewers share one UDP socket; overview 3.2, changes 11, 16 and 17) rather than packetizing alone.
- The Go server stays at 0–3% of a core: signaling and session tracking are not the limit.
- A new viewer's first keyframe arrives after about 235 ms (median), 0.6 s at the 95th percentile and 1.3 s at worst, while 10 viewers join per second.
- With `CUPSTREAM_EGRESS_INTERFACE=lo CUPSTREAM_EGRESS_LIMIT_MBPS=100`, admission control let 18 viewers of a 4.2 Mbit/s stream in and refused the other 22 with 503 `server_full`.

Loopback is not the production NIC, and the laptop is not the production server. The plateau should be measured again on the server's own hardware before relying on the 800 Mbit/s admission limit.

### Several UDP sockets (30 October 2026)

A goroutine dump at the plateau showed every reader's writer (150 of 150) waiting on the shared socket's write lock, with one in `sendto`: all viewers' packets went out one at a time. `v1.21.2-cupstream.4` adds `webrtcLocalUDPSockets` (RAdu2005/mediamtx#1). Same laptop, MediaMTX run natively; 1 stream × 150 viewers, 8.2 Mbit/s 1080p60:

| Build | Out of MediaMTX | Starved | Frames discarded | MediaMTX CPU | First keyframe (median / p95) |
|---|---|---|---|---|---|
| `cupstream.3`, one socket | ~970 Mbit/s plateau | ~75 of 150 | many ("reader is too slow") | ~1.3 cores | 234 / 545 ms |
| `cupstream.4`, `webrtcLocalUDPSockets: 8` | ~1.25 Gbit/s, everything | 0 | none | ~2 cores | 135 / 336 ms |

That is beyond the production server's 1 Gbit/s link, so the network is the limit again. Changes 16 and 17 (packetizing once per stream, pacing) are not needed for capacity and stay deferred. Pacing could still smooth key frames reaching many viewers at once on real links, which loopback cannot show.
