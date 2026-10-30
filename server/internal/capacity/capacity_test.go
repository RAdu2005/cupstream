package capacity

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeSys makes a /sys/class/net with the interfaces in tx (bytes sent so
// far); the physical ones get a device entry.
func fakeSys(t *testing.T, physical []string, virtual []string) func(name string, tx uint64) {
	t.Helper()
	dir := t.TempDir()
	old := sysNet
	sysNet = dir
	t.Cleanup(func() { sysNet = old })
	set := func(name string, tx uint64) {
		os.MkdirAll(filepath.Join(dir, name, "statistics"), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name, "statistics", "tx_bytes"), []byte(strconv.FormatUint(tx, 10)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range physical {
		set(name, 0)
		os.WriteFile(filepath.Join(dir, name, "device"), nil, 0o644)
	}
	for _, name := range virtual {
		set(name, 0)
	}
	return set
}

func TestMeter(t *testing.T) {
	set := fakeSys(t, []string{"eno1", "eno2"}, []string{"lo", "docker0"})
	m, err := New(nil, 100_000_000) // 100 Mbps
	if err != nil {
		t.Fatal(err)
	}
	if u := m.Usage(); len(u.Interfaces) != 2 || u.Interfaces[0] != "eno1" || u.Interfaces[1] != "eno2" {
		t.Fatalf("interfaces %v, want the physical ones", u.Interfaces)
	}
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	m.sample()

	// 7.5 MB in a second on the two interfaces, plus traffic on lo that
	// does not count: 60 Mbps.
	set("eno1", 5_000_000)
	set("eno2", 2_500_000)
	set("lo", 1<<40)
	now = now.Add(time.Second)
	m.sample()
	if u := m.Usage(); u.Rate != 60_000_000 || u.Refusing {
		t.Fatalf("usage %+v, want 60 Mbps", u)
	}

	// 60 + 25 fits, the next 25 Mbps viewer does not: the first one is not
	// measured yet but counts.
	if !m.Admit(25_000_000) {
		t.Fatal("first viewer refused")
	}
	if m.Admit(25_000_000) {
		t.Fatal("viewer over the limit admitted")
	}
	if u := m.Usage(); u.Reserved != 25_000_000 || !u.Refusing {
		t.Fatalf("usage %+v", u)
	}
	// A stream without a bitrate yet counts as 8 Mbps: 60 + 25 + 8 fits.
	if !m.Admit(0) || m.Usage().Reserved != 33_000_000 {
		t.Fatalf("usage %+v", m.Usage())
	}

	// Once measured, reservations expire; traffic going down makes room.
	set("eno1", 6_000_000)
	now = now.Add(reserveFor)
	m.sample()
	if u := m.Usage(); u.Rate != 1_600_000 || u.Reserved != 0 || u.Refusing {
		t.Fatalf("usage %+v", u)
	}
	if !m.Admit(25_000_000) {
		t.Fatal("refused with room to spare")
	}
}

func TestMeterKeepsRateWhenReadingFails(t *testing.T) {
	set := fakeSys(t, nil, []string{"eth0"})
	m, err := New([]string{"eth0"}, 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	m.sample()
	set("eth0", 2_000_000)
	now = now.Add(time.Second)
	m.sample()
	os.RemoveAll(filepath.Join(sysNet, "eth0"))
	now = now.Add(time.Second)
	m.sample()
	if m.Usage().Rate != 16_000_000 || m.Admit(1) {
		t.Fatalf("usage %+v after the interface vanished", m.Usage())
	}
}

func TestNewErrors(t *testing.T) {
	fakeSys(t, nil, []string{"lo"})
	if _, err := New(nil, 1); err == nil {
		t.Error("no physical interface: no error")
	}
	if _, err := New([]string{"eth9"}, 1); err == nil {
		t.Error("unknown interface: no error")
	}
	if _, err := New([]string{"../lo"}, 1); err == nil {
		t.Error("path as interface name: no error")
	}
}
