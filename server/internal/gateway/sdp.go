package gateway

import (
	"errors"
	"slices"
	"strings"
)

// ErrNoVideoCodec is returned when a publisher offers only video codecs that
// viewers cannot be relied on to decode.
var ErrNoVideoCodec = errors.New("HEVC (H.265) is not supported: send H.264 or AV1")

// refusedVideo are video codecs removed from publish offers. MediaMTX does
// not transcode, so every viewer must decode what the sender sends, and
// HEVC playback in browsers is unreliable.
var refusedVideo = []string{"h265", "hevc"}

// filterPublishOffer removes refused video codecs (and their RTX payload
// types) from an SDP offer. It fails if a video section has nothing left.
func filterPublishOffer(sdp string) (string, error) {
	eol := "\r\n"
	if !strings.Contains(sdp, "\r\n") {
		eol = "\n"
	}
	lines := strings.Split(strings.TrimRight(sdp, "\r\n"), eol)

	// Split into the session part and media sections.
	var sections [][]string
	cur := []string{}
	for _, l := range lines {
		if strings.HasPrefix(l, "m=") {
			sections = append(sections, cur)
			cur = []string{}
		}
		cur = append(cur, l)
	}
	sections = append(sections, cur)

	for i, sec := range sections[1:] {
		if !strings.HasPrefix(sec[0], "m=video ") {
			continue
		}
		filtered, err := filterVideoSection(sec)
		if err != nil {
			return "", err
		}
		sections[i+1] = filtered
	}

	var out []string
	for _, sec := range sections {
		out = append(out, sec...)
	}
	return strings.Join(out, eol) + eol, nil
}

func filterVideoSection(sec []string) ([]string, error) {
	remove := map[string]bool{}
	for _, l := range sec {
		pt, codec, ok := rtpmap(l)
		if ok && slices.Contains(refusedVideo, strings.ToLower(codec)) {
			remove[pt] = true
		}
	}
	if len(remove) == 0 {
		return sec, nil
	}
	// RTX payload types that repair a removed codec go too.
	for _, l := range sec {
		if pt, params, ok := strings.Cut(strings.TrimPrefix(l, "a=fmtp:"), " "); ok && strings.HasPrefix(l, "a=fmtp:") {
			for _, p := range strings.Split(params, ";") {
				if k, v, _ := strings.Cut(strings.TrimSpace(p), "="); k == "apt" && remove[v] {
					remove[pt] = true
				}
			}
		}
	}

	fields := strings.Fields(sec[0]) // m=video <port> <proto> <pt>...
	kept := fields[:3]
	for _, pt := range fields[3:] {
		if !remove[pt] {
			kept = append(kept, pt)
		}
	}
	if len(kept) == 3 {
		return nil, ErrNoVideoCodec
	}

	out := []string{strings.Join(kept, " ")}
	for _, l := range sec[1:] {
		if pt, ok := attrPayloadType(l); ok && remove[pt] {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// rtpmap parses "a=rtpmap:<pt> <codec>/<clock>...".
func rtpmap(l string) (pt, codec string, ok bool) {
	rest, ok := strings.CutPrefix(l, "a=rtpmap:")
	if !ok {
		return "", "", false
	}
	pt, enc, ok := strings.Cut(rest, " ")
	if !ok {
		return "", "", false
	}
	codec, _, _ = strings.Cut(enc, "/")
	return pt, codec, true
}

// attrPayloadType returns the payload type of rtpmap, fmtp and rtcp-fb lines.
func attrPayloadType(l string) (string, bool) {
	for _, prefix := range []string{"a=rtpmap:", "a=fmtp:", "a=rtcp-fb:"} {
		if rest, ok := strings.CutPrefix(l, prefix); ok {
			pt, _, _ := strings.Cut(rest, " ")
			return pt, true
		}
	}
	return "", false
}
