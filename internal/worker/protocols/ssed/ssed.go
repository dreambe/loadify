// Package ssed implements the Server-Sent-Events load-generation Driver. One
// Exec opens an event stream, consumes up to max_events (or until the timeout /
// stream end), and records time-to-first-event and total stream duration.
package ssed

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

func init() {
	protocols.Register(loadifyv1.Protocol_PROTOCOL_SSE, factory)
}

func factory(p *plan.Plan) (protocols.Driver, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Driver{cfg: p.SSE}, nil
}

// Driver streams Server-Sent-Events using a shared, tuned transport.
type Driver struct {
	cfg     *plan.SSEConfig
	client  *http.Client
	group   string
	timeout time.Duration
}

// Prepare builds the shared HTTP client.
func (d *Driver) Prepare(_ context.Context) error {
	d.timeout = time.Duration(d.cfg.TimeoutMs) * time.Millisecond
	if d.timeout == 0 {
		d.timeout = 30 * time.Second
	}
	tr := &http.Transport{
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 1024,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: d.cfg.InsecureSkipVerify}, //nolint:gosec // opt-in only, defaults to verifying
	}
	// No client-level timeout: streams are long-lived; we bound each Exec via ctx.
	d.client = &http.Client{Transport: tr}
	d.group = d.cfg.Group
	if d.group == "" {
		d.group = "default"
	}
	return nil
}

// Exec opens one SSE stream and reads events.
func (d *Driver) Exec(ctx context.Context, _ *protocols.VU) protocols.Result {
	res := protocols.Result{Group: d.group, Method: http.MethodGet, URL: d.cfg.URL}

	opCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(opCtx, http.MethodGet, d.cfg.URL, nil)
	if err != nil {
		res.ErrorKind = "build_request"
		return res
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range d.cfg.Headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := d.client.Do(req)
	if err != nil {
		res.LatencyUs = time.Since(start).Microseconds()
		res.ErrorKind = classifyErr(err)
		return res
	}
	defer resp.Body.Close()

	res.Status = int32(resp.StatusCode)
	if resp.StatusCode >= 400 {
		res.LatencyUs = time.Since(start).Microseconds()
		res.ErrorKind = "unexpected_status"
		return res
	}

	maxEvents := d.cfg.MaxEvents
	if maxEvents <= 0 {
		maxEvents = 1
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var events, recv int64
	var firstEventAt time.Time
	for sc.Scan() {
		line := sc.Text()
		recv += int64(len(line)) + 1
		// An event is delimited by a blank line; "data:" lines carry payload.
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if firstEventAt.IsZero() {
				firstEventAt = time.Now()
				res.TTFBUs = firstEventAt.Sub(start).Microseconds()
				body := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if len(body) > protocols.RespBodyCap {
					body = body[:protocols.RespBodyCap]
				}
				res.RespBody = body
			}
			events++
			if int(events) >= maxEvents {
				break
			}
		}
		if opCtx.Err() != nil {
			break
		}
	}
	res.LatencyUs = time.Since(start).Microseconds()
	res.RecvBytes = recv

	if err := sc.Err(); err != nil && opCtx.Err() == nil {
		res.ErrorKind = classifyErr(err)
		return res
	}
	if events == 0 {
		res.ErrorKind = "no_events"
		return res
	}
	res.OK = true
	return res
}

// Teardown closes idle connections.
func (d *Driver) Teardown(_ context.Context) error {
	if d.client != nil {
		d.client.CloseIdleConnections()
	}
	return nil
}

func classifyErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Timeout"):
		return "timeout"
	case strings.Contains(s, "connection refused"):
		return "conn_refused"
	case strings.Contains(s, "no such host"):
		return "dns"
	case strings.Contains(s, "EOF"):
		return "eof"
	default:
		return "transport"
	}
}
