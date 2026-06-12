// Package httpd implements the HTTP/HTTPS load-generation Driver.
package httpd

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

func init() {
	protocols.Register(loadifyv1.Protocol_PROTOCOL_HTTP, factory)
	protocols.Register(loadifyv1.Protocol_PROTOCOL_HTTPS, factory)
}

// assertBodyCap bounds how much of the response body is buffered when a
// body-contains assertion is configured.
const assertBodyCap = 256 << 10

func factory(p *plan.Plan) (protocols.Driver, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Driver{cfg: p.HTTP}, nil
}

// Driver is an HTTP/HTTPS load driver backed by a tuned shared transport.
type Driver struct {
	cfg    *plan.HTTPConfig
	client *http.Client
	group  string
}

// Prepare builds the shared http.Client/Transport for the run.
func (d *Driver) Prepare(_ context.Context) error {
	timeout := time.Duration(d.cfg.TimeoutMs) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	tr := &http.Transport{
		MaxIdleConns:          0, // unlimited
		MaxIdleConnsPerHost:   1024,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     d.cfg.DisableKeepAlive,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: d.cfg.InsecureSkipVerify}, //nolint:gosec // opt-in only, defaults to verifying
		ExpectContinueTimeout: 1 * time.Second,
	}
	d.client = &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	d.group = d.cfg.Group
	if d.group == "" {
		d.group = "default"
	}
	return nil
}

// Exec performs one HTTP request and captures phase timings via httptrace.
func (d *Driver) Exec(ctx context.Context, _ *protocols.VU) protocols.Result {
	res := protocols.Result{Group: d.group, Method: d.cfg.Method, URL: d.cfg.URL}

	var body io.Reader
	if d.cfg.Body != "" {
		body = strings.NewReader(d.cfg.Body)
	}
	req, err := http.NewRequestWithContext(ctx, d.cfg.Method, d.cfg.URL, body)
	if err != nil {
		res.ErrorKind = "build_request"
		return res
	}
	for k, v := range d.cfg.Headers {
		req.Header.Set(k, v)
	}
	res.SentBytes = int64(len(d.cfg.Body))

	// Phase timings are populated from httptrace callbacks, which the transport
	// may invoke on background dial goroutines (including losing parallel dials)
	// concurrently with this goroutine. Guard all of that shared state with a
	// mutex so reads after Do are race-free.
	ph := &phase{}
	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { ph.markStart(&ph.dnsStart) },
		DNSDone:              func(httptrace.DNSDoneInfo) { ph.markDone(&ph.dnsStart, &ph.dnsUs) },
		ConnectStart:         func(_, _ string) { ph.markStart(&ph.connStart) },
		ConnectDone:          func(_, _ string, _ error) { ph.markDone(&ph.connStart, &ph.connectUs) },
		TLSHandshakeStart:    func() { ph.markStart(&ph.tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { ph.markDone(&ph.tlsStart, &ph.tlsUs) },
		GotFirstResponseByte: func() { ph.markFirstByte() },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	start := time.Now()
	resp, err := d.client.Do(req)
	if err != nil {
		res.LatencyUs = sinceUs(start)
		res.ErrorKind = classifyErr(err)
		return res
	}
	defer resp.Body.Close()
	// Keep a head of the body for the live response log and assertions, drain
	// the rest. Body assertions need more context than the log snippet.
	capN := protocols.RespBodyCap
	if d.cfg.BodyContains != "" || len(d.cfg.Asserts) > 0 {
		capN = assertBodyCap
	}
	head := make([]byte, capN)
	hn, _ := io.ReadFull(resp.Body, head)
	rest, _ := io.Copy(io.Discard, resp.Body)
	n := int64(hn) + rest
	snip := hn
	if snip > protocols.RespBodyCap {
		snip = protocols.RespBodyCap
	}
	res.RespBody = string(head[:snip])
	res.LatencyUs = sinceUs(start)
	dnsUs, connectUs, tlsUs, firstByte := ph.snapshot()
	res.DNSUs, res.ConnectUs, res.TLSUs = dnsUs, connectUs, tlsUs
	if !firstByte.IsZero() {
		res.TTFBUs = firstByte.Sub(start).Microseconds()
	}
	res.RecvBytes = n
	res.Status = int32(resp.StatusCode)
	res.OK = resp.StatusCode < 400
	if d.cfg.ExpectStatus != 0 {
		res.OK = resp.StatusCode == d.cfg.ExpectStatus
		if !res.OK {
			res.ErrorKind = "unexpected_status"
		}
	}
	if res.OK && d.cfg.BodyContains != "" && !strings.Contains(string(head[:hn]), d.cfg.BodyContains) {
		res.OK = false
		res.ErrorKind = "assert_body"
	}
	if res.OK && len(d.cfg.Asserts) > 0 {
		if reason := evalAsserts(d.cfg.Asserts, resp.StatusCode, head[:hn]); reason != "" {
			res.OK = false
			res.ErrorKind = reason
		}
	}
	return res
}

// Teardown closes idle connections.
func (d *Driver) Teardown(_ context.Context) error {
	if d.client != nil {
		d.client.CloseIdleConnections()
	}
	return nil
}

// phase accumulates httptrace phase timings safely across the goroutines the
// HTTP transport may use for dialing and reading.
type phase struct {
	mu                          sync.Mutex
	dnsStart, connStart, tlsStart, firstByte time.Time
	dnsUs, connectUs, tlsUs     int64
}

func (p *phase) markStart(field *time.Time) {
	p.mu.Lock()
	*field = time.Now()
	p.mu.Unlock()
}

func (p *phase) markDone(start *time.Time, out *int64) {
	p.mu.Lock()
	*out = sinceUs(*start)
	p.mu.Unlock()
}

func (p *phase) markFirstByte() {
	p.mu.Lock()
	if p.firstByte.IsZero() {
		p.firstByte = time.Now()
	}
	p.mu.Unlock()
}

func (p *phase) snapshot() (dnsUs, connectUs, tlsUs int64, firstByte time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dnsUs, p.connectUs, p.tlsUs, p.firstByte
}

func sinceUs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return time.Since(t).Microseconds()
}

func classifyErr(err error) string {
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
