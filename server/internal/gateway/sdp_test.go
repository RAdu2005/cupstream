package gateway

import (
	"strings"
	"testing"
)

const offer = "v=0\r\n" +
	"o=- 1 2 IN IP4 127.0.0.1\r\n" +
	"s=-\r\n" +
	"t=0 0\r\n" +
	"m=video 9 UDP/TLS/RTP/SAVPF 96 97 98 99\r\n" +
	"a=mid:0\r\n" +
	"a=rtpmap:96 H264/90000\r\n" +
	"a=fmtp:96 packetization-mode=1;profile-level-id=42e01f\r\n" +
	"a=rtcp-fb:96 nack\r\n" +
	"a=rtpmap:97 rtx/90000\r\n" +
	"a=fmtp:97 apt=96\r\n" +
	"a=rtpmap:98 H265/90000\r\n" +
	"a=rtcp-fb:98 nack pli\r\n" +
	"a=rtpmap:99 rtx/90000\r\n" +
	"a=fmtp:99 apt=98\r\n" +
	"m=audio 9 UDP/TLS/RTP/SAVPF 111\r\n" +
	"a=mid:1\r\n" +
	"a=rtpmap:111 opus/48000/2\r\n"

func TestFilterPublishOffer(t *testing.T) {
	got, err := filterPublishOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "m=video 9 UDP/TLS/RTP/SAVPF 96 97\r\n") {
		t.Errorf("m-line not filtered:\n%s", got)
	}
	for _, gone := range []string{"H265", "a=rtcp-fb:98", "a=fmtp:99", "a=rtpmap:99"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q still present:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"a=rtpmap:96 H264/90000", "a=fmtp:97 apt=96", "a=rtpmap:111 opus/48000/2", "m=audio 9 UDP/TLS/RTP/SAVPF 111"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q missing:\n%s", kept, got)
		}
	}
	if !strings.HasSuffix(got, "\r\n") {
		t.Error("lost the final line ending")
	}
}

func TestFilterPublishOfferUnchanged(t *testing.T) {
	h264only := strings.ReplaceAll(offer, "H265", "AV1")
	if got, err := filterPublishOffer(h264only); err != nil || got != h264only {
		t.Fatalf("offer without HEVC changed (err %v):\n%s", err, got)
	}
}

func TestFilterPublishOfferHEVCOnly(t *testing.T) {
	hevc := "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 98\r\na=rtpmap:98 H265/90000\r\n"
	if _, err := filterPublishOffer(hevc); err != ErrNoVideoCodec {
		t.Fatalf("got %v, want ErrNoVideoCodec", err)
	}
}
