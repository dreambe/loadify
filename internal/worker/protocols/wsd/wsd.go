// Package wsd implements the WebSocket load-generation Driver. Each virtual user
// keeps a persistent connection for the lifetime of its iterations; one Exec
// sends the next scripted frame and, when expect_echo is set, awaits the reply,
// recording connect time, time-to-first-byte and round-trip latency.
package wsd

import (
	"context"
	"strings"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
	"github.com/coder/websocket"
)

func init() {
	protocols.Register(loadifyv1.Protocol_PROTOCOL_WEBSOCKET, factory)
}

func factory(p *plan.Plan) (protocols.Driver, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Driver{cfg: p.WS}, nil
}

// Driver drives WebSocket load. It is safe for concurrent Exec across VUs; each
// VU's connection is isolated in the conns map keyed by VU id.
type Driver struct {
	cfg     *plan.WSConfig
	group   string
	timeout time.Duration

	mu    sync.Mutex
	conns map[int]*websocket.Conn
}

// Prepare initialises per-VU connection bookkeeping.
func (d *Driver) Prepare(_ context.Context) error {
	d.conns = make(map[int]*websocket.Conn)
	d.group = d.cfg.Group
	if d.group == "" {
		d.group = "default"
	}
	d.timeout = 30 * time.Second
	return nil
}

// Exec performs one WebSocket round-trip for the VU. It dials lazily on the
// first iteration and reuses the connection afterwards; on any error the
// connection is dropped so the next iteration redials.
func (d *Driver) Exec(ctx context.Context, vu *protocols.VU) protocols.Result {
	res := protocols.Result{Group: d.group}

	opCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	conn, fresh, connectUs, err := d.connFor(opCtx, vu.ID)
	if err != nil {
		res.LatencyUs = connectUs
		res.ErrorKind = classifyErr(err)
		return res
	}
	if fresh {
		res.ConnectUs = connectUs
	}

	payload := d.payload(vu.Iteration)
	res.SentBytes = int64(len(payload))

	start := time.Now()
	if err := conn.Write(opCtx, websocket.MessageText, payload); err != nil {
		d.drop(vu.ID, conn)
		res.LatencyUs = time.Since(start).Microseconds()
		res.ErrorKind = classifyErr(err)
		return res
	}

	if d.cfg.ExpectEcho {
		_, data, rerr := conn.Read(opCtx)
		res.LatencyUs = time.Since(start).Microseconds()
		res.TTFBUs = res.LatencyUs
		if rerr != nil {
			d.drop(vu.ID, conn)
			res.ErrorKind = classifyErr(rerr)
			return res
		}
		res.RecvBytes = int64(len(data))
		res.OK = true
		return res
	}

	res.LatencyUs = time.Since(start).Microseconds()
	res.OK = true
	return res
}

// Teardown closes every live VU connection.
func (d *Driver) Teardown(_ context.Context) error {
	d.mu.Lock()
	conns := d.conns
	d.conns = make(map[int]*websocket.Conn)
	d.mu.Unlock()
	for _, c := range conns {
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
	return nil
}

// connFor returns the VU's connection, dialing if necessary. The returned
// connectUs is non-zero only when a fresh dial occurred.
func (d *Driver) connFor(ctx context.Context, vuID int) (conn *websocket.Conn, fresh bool, connectUs int64, err error) {
	d.mu.Lock()
	c := d.conns[vuID]
	d.mu.Unlock()
	if c != nil {
		return c, false, 0, nil
	}

	opts := &websocket.DialOptions{}
	if len(d.cfg.Headers) > 0 {
		opts.HTTPHeader = make(map[string][]string, len(d.cfg.Headers))
		for k, v := range d.cfg.Headers {
			opts.HTTPHeader.Set(k, v)
		}
	}
	start := time.Now()
	c, _, err = websocket.Dial(ctx, d.cfg.URL, opts)
	connectUs = time.Since(start).Microseconds()
	if err != nil {
		return nil, false, connectUs, err
	}
	// Allow large echoed frames.
	c.SetReadLimit(8 << 20)
	d.mu.Lock()
	d.conns[vuID] = c
	d.mu.Unlock()
	return c, true, connectUs, nil
}

func (d *Driver) drop(vuID int, conn *websocket.Conn) {
	d.mu.Lock()
	if d.conns[vuID] == conn {
		delete(d.conns, vuID)
	}
	d.mu.Unlock()
	_ = conn.Close(websocket.StatusInternalError, "")
}

// payload selects the frame to send for the given iteration. With multiple
// configured messages it cycles through them; with none it sends a ping text.
func (d *Driver) payload(iteration int64) []byte {
	msgs := d.cfg.SendMessages
	if len(msgs) == 0 {
		return []byte("ping")
	}
	return []byte(msgs[int(iteration)%len(msgs)])
}

func classifyErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "timeout"):
		return "timeout"
	case strings.Contains(s, "connection refused"):
		return "conn_refused"
	case strings.Contains(s, "no such host"):
		return "dns"
	case strings.Contains(s, "EOF"), strings.Contains(s, "closed"):
		return "closed"
	default:
		return "transport"
	}
}
