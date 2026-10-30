package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func TestViewerReports(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	st := f.publish(t)
	f.reconcile(t)
	v := f.play(t, st, f.user.ID, "tab=tab-one-1", http.StatusCreated)
	rep := api.ViewerReport{Streams: []api.StreamReport{{StreamID: st.Stream.ID, BitrateBps: 2e6, LossRatio: -3}}}

	if n := f.gw.Report(f.user.ID, "tab-two-2", rep); n != 0 {
		t.Fatalf("another tab's report matched %d sessions", n)
	}
	if n := f.gw.Report(f.user.ID, "tab-one-1", rep); n != 1 {
		t.Fatalf("matched %d sessions, want 1", n)
	}
	got := f.gw.ViewerReports(st.Stream.ID)
	if len(got) != 1 || got[0].Stream.BitrateBps != 2e6 || got[0].Stream.LossRatio != 0 {
		t.Fatalf("reports = %+v", got)
	}

	// Paused sessions are not watching; old reports are stale.
	f.control(t, st, v, "pause", "")
	if got := f.gw.ViewerReports(st.Stream.ID); len(got) != 0 {
		t.Fatalf("a paused viewer's report: %+v", got)
	}
	f.control(t, st, v, "resume", "")
	f.clock = f.clock.Add(reportTTL + 1)
	if got := f.gw.ViewerReports(st.Stream.ID); len(got) != 0 {
		t.Fatalf("a stale report: %+v", got)
	}
}
