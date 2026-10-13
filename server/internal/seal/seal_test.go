package seal

import (
	"bytes"
	"testing"
	"time"
)

func TestSeal(t *testing.T) {
	s, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed := s.Seal("token", []byte("secret"))
	if got, err := s.Open("token", sealed); err != nil || string(got) != "secret" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open("cookie", sealed); err == nil {
		t.Fatal("opened with the wrong purpose")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.Open("token", sealed); err == nil {
		t.Fatal("opened a tampered value")
	}

	type flow struct{ State string }
	str, err := SealJSON(s, "flow", flow{"abc"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := OpenJSON[flow](s, "flow", str); err != nil || f.State != "abc" {
		t.Fatalf("OpenJSON = %+v, %v", f, err)
	}
	expired, _ := SealJSON(s, "flow", flow{"abc"}, -time.Second)
	if _, err := OpenJSON[flow](s, "flow", expired); err == nil {
		t.Fatal("opened an expired value")
	}

	if _, err := New([]byte("short")); err == nil {
		t.Fatal("accepted a short key")
	}
}
