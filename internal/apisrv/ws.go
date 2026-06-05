package apisrv

import (
	"context"
	"net/http"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
)

// liveTick is the JSON shape pushed to the browser.
type liveTick struct {
	RunID     string             `json:"run_id"`
	TS        int64              `json:"ts_unix_ms"`
	RPS       float64            `json:"rps"`
	ErrorRate float64            `json:"error_rate"`
	ActiveVUs int64              `json:"active_vus"`
	P50ms     float64            `json:"p50_ms"`
	P90ms     float64            `json:"p90_ms"`
	P95ms     float64            `json:"p95_ms"`
	P99ms     float64            `json:"p99_ms"`
	Groups    map[string]grpTick `json:"groups,omitempty"`
}

type grpTick struct {
	RPS       float64 `json:"rps"`
	ErrorRate float64 `json:"error_rate"`
	P50ms     float64 `json:"p50_ms"`
	P90ms     float64 `json:"p90_ms"`
	P95ms     float64 `json:"p95_ms"`
	P99ms     float64 `json:"p99_ms"`
}

// handleRunLive upgrades to a WebSocket and forwards coordinator live ticks.
func (s *Server) handleRunLive(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream, err := s.coord.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: runID})
	if err != nil {
		conn.Close(websocket.StatusInternalError, "stream unavailable")
		return
	}

	// Detect client disconnect.
	go func() {
		conn.Read(ctx) //nolint:errcheck // we only care that it unblocks on close
		cancel()
	}()

	for {
		tick, err := stream.Recv()
		if err != nil {
			return
		}
		out := toLiveTick(tick)
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		err = wsjson.Write(wctx, conn, out)
		wcancel()
		if err != nil {
			return
		}
	}
}

func toLiveTick(t *loadifyv1.LiveTick) liveTick {
	groups := make(map[string]grpTick, len(t.Groups))
	for name, g := range t.Groups {
		groups[name] = grpTick{RPS: g.Rps, ErrorRate: g.ErrorRate, P50ms: g.P50Ms, P90ms: g.P90Ms, P95ms: g.P95Ms, P99ms: g.P99Ms}
	}
	return liveTick{
		RunID:     t.RunId,
		TS:        t.TsUnixMs,
		RPS:       t.Rps,
		ErrorRate: t.ErrorRate,
		ActiveVUs: t.ActiveVus,
		P50ms:     t.P50Ms,
		P90ms:     t.P90Ms,
		P95ms:     t.P95Ms,
		P99ms:     t.P99Ms,
		Groups:    groups,
	}
}
