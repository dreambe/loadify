// Package httpd implements the HTTP/HTTPS load-generation Driver.
package httpd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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
	cfg     *plan.HTTPConfig
	client  *http.Client
	tr      *http.Transport
	timeout time.Duration
	group   string
	url     string // cfg.URL with structured query params appended

	// Per-VU clients (own cookie jar, shared transport) when CookieJar is on,
	// so each virtual user keeps its own session.
	jars sync.Map // vuID(int) -> *http.Client
}

// redirectPolicy returns the CheckRedirect for the configured follow behavior.
func (d *Driver) redirectPolicy() func(*http.Request, []*http.Request) error {
	if d.cfg.FollowRedirects {
		return nil // default Go behavior: follow up to 10 redirects
	}
	return func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
}

// clientFor returns the request client for a VU: a per-VU cookie-jar client
// when CookieJar is enabled, else the shared client.
func (d *Driver) clientFor(vuID int) *http.Client {
	if !d.cfg.CookieJar {
		return d.client
	}
	if c, ok := d.jars.Load(vuID); ok {
		return c.(*http.Client)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Transport: d.tr, Timeout: d.timeout, Jar: jar, CheckRedirect: d.redirectPolicy()}
	actual, _ := d.jars.LoadOrStore(vuID, c)
	return actual.(*http.Client)
}

// Prepare builds the shared http.Client/Transport for the run.
func (d *Driver) Prepare(_ context.Context) error {
	timeout := time.Duration(d.cfg.TimeoutMs) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: d.cfg.InsecureSkipVerify} //nolint:gosec // opt-in only, defaults to verifying
	// Mutual TLS: present a client certificate when configured.
	if d.cfg.ClientCertPEM != "" || d.cfg.ClientKeyPEM != "" {
		cert, err := tls.X509KeyPair([]byte(d.cfg.ClientCertPEM), []byte(d.cfg.ClientKeyPEM))
		if err != nil {
			return fmt.Errorf("httpd: invalid client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	tr := &http.Transport{
		MaxIdleConns:          0, // unlimited
		MaxIdleConnsPerHost:   1024,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     d.cfg.DisableKeepAlive,
		TLSClientConfig:       tlsCfg,
		ExpectContinueTimeout: 1 * time.Second,
	}
	d.tr = tr
	d.timeout = timeout
	d.client = &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: d.redirectPolicy()}

	// Resolve structured query params once (values are static post env-substitution).
	d.url = d.cfg.URL
	if len(d.cfg.Params) > 0 {
		qs := url.Values{}
		for _, p := range d.cfg.Params {
			if p.Key != "" {
				qs.Add(p.Key, p.Value)
			}
		}
		if enc := qs.Encode(); enc != "" {
			sep := "?"
			if strings.Contains(d.url, "?") {
				sep = "&"
			}
			d.url += sep + enc
		}
	}

	d.group = d.cfg.Group
	if d.group == "" {
		d.group = "default"
	}
	return nil
}

// Exec performs one HTTP request and captures phase timings via httptrace.
func (d *Driver) Exec(ctx context.Context, vu *protocols.VU) protocols.Result {
	res := protocols.Result{Group: d.group, Method: d.cfg.Method, URL: d.url}

	var body io.Reader
	if d.cfg.Body != "" {
		body = strings.NewReader(d.cfg.Body)
	}
	req, err := http.NewRequestWithContext(ctx, d.cfg.Method, d.url, body)
	if err != nil {
		res.ErrorKind = "build_request"
		return res
	}
	for k, v := range d.cfg.Headers {
		req.Header.Set(k, v)
	}
	res.SentBytes = int64(len(d.cfg.Body))
	if reqBody := d.cfg.Body; reqBody != "" {
		if len(reqBody) > protocols.RespBodyCap {
			reqBody = reqBody[:protocols.RespBodyCap]
		}
		res.ReqBody = reqBody
	}

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

	vuID := 0
	if vu != nil {
		vuID = vu.ID
	}
	start := time.Now()
	resp, err := d.clientFor(vuID).Do(req)
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
// HTTP transport may use for dialing and reading. Each field is written at most
// once per request from a single callback, so lock-free atomics suffice and
// keep the hot path free of mutex contention. Times are stored as Unix nanos.
type phase struct {
	dnsStart, connStart, tlsStart atomic.Int64 // phase-start timestamps
	dnsUs, connectUs, tlsUs       atomic.Int64 // measured phase durations (µs)
	firstByte                     atomic.Int64 // first-response-byte timestamp
}

func (p *phase) markStart(field *atomic.Int64) {
	field.Store(time.Now().UnixNano())
}

func (p *phase) markDone(start, out *atomic.Int64) {
	if s := start.Load(); s != 0 {
		out.Store((time.Now().UnixNano() - s) / 1e3)
	}
}

func (p *phase) markFirstByte() {
	p.firstByte.CompareAndSwap(0, time.Now().UnixNano())
}

func (p *phase) snapshot() (dnsUs, connectUs, tlsUs int64, firstByte time.Time) {
	if fb := p.firstByte.Load(); fb != 0 {
		firstByte = time.Unix(0, fb)
	}
	return p.dnsUs.Load(), p.connectUs.Load(), p.tlsUs.Load(), firstByte
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
