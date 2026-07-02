// Package httpd implements the HTTP/HTTPS load-generation Driver.
package httpd

import (
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
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
	"github.com/dreambe/loadify/internal/vars"
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
	return &Driver{cfg: p.HTTP, feed: p.Dataset}, nil
}

// Driver is an HTTP/HTTPS load driver backed by a tuned shared transport.
type Driver struct {
	cfg     *plan.HTTPConfig
	client  *http.Client
	tr      *http.Transport
	timeout time.Duration
	group   string
	url     string // cfg.URL with structured query params appended

	// Dynamic parameters: feed rows cycle per request (shared counter across
	// this worker's VUs, so concurrent VUs draw different rows) and dynamic
	// marks that the request template must be re-rendered per request —
	// because a feed exists or the template itself contains {{...}} tokens
	// (dataset columns or built-in generators like {{uuid}}).
	feed    []map[string]any
	feedIdx atomic.Uint64
	dynamic bool

	// Per-VU clients (own cookie jar, shared transport) when CookieJar is on,
	// so each virtual user keeps its own session.
	jars sync.Map // vuID(int) -> *http.Client
}

// newTraceparent builds a W3C traceparent: version 00, random 16-byte trace id,
// random 8-byte span id, sampled flag (01).
func newTraceparent() string {
	var b [24]byte
	_, _ = crand.Read(b[:])
	return "00-" + hex.EncodeToString(b[:16]) + "-" + hex.EncodeToString(b[16:24]) + "-01"
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

	// A request is dynamic when a data feed is present or any field carries a
	// {{...}} token; then URL/params/headers/body are rendered per request.
	d.dynamic = len(d.feed) > 0 || vars.Has(d.cfg.URL) || vars.Has(d.cfg.Body) || anyTemplated(d.cfg)

	// Resolve structured query params once (values are static post
	// env-substitution) — only for static requests; dynamic ones interpolate
	// values first and encode per request.
	d.url = d.cfg.URL
	if !d.dynamic && len(d.cfg.Params) > 0 {
		d.url = appendParams(d.cfg.URL, d.cfg.Params, nil)
	}

	d.group = d.cfg.Group
	if d.group == "" {
		d.group = "default"
	}
	return nil
}

// anyTemplated reports whether any param or header value/key carries a
// {{...}} token.
func anyTemplated(cfg *plan.HTTPConfig) bool {
	for _, p := range cfg.Params {
		if vars.Has(p.Key) || vars.Has(p.Value) {
			return true
		}
	}
	for k, v := range cfg.Headers {
		if vars.Has(k) || vars.Has(v) {
			return true
		}
	}
	return false
}

// appendParams interpolates (when row != nil) and URL-encodes structured query
// params onto base. Interpolate THEN encode, so {{var}} resolves before
// escaping — mirroring the scenario harness.
func appendParams(base string, params []plan.ScenarioParam, row map[string]any) string {
	if len(params) == 0 {
		return base
	}
	qs := url.Values{}
	for _, p := range params {
		if p.Key == "" {
			continue
		}
		if row != nil || vars.Has(p.Key) || vars.Has(p.Value) {
			qs.Add(vars.Interp(p.Key, row), vars.Interp(p.Value, row))
		} else {
			qs.Add(p.Key, p.Value)
		}
	}
	enc := qs.Encode()
	if enc == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + enc
}

// nextRow returns the next data row (cycling; nil without a feed). The shared
// counter means this worker's VUs collectively walk the dataset in order.
func (d *Driver) nextRow() map[string]any {
	if len(d.feed) == 0 {
		return nil
	}
	n := d.feedIdx.Add(1) - 1
	return d.feed[n%uint64(len(d.feed))]
}

// Exec performs one HTTP request and captures phase timings via httptrace.
func (d *Driver) Exec(ctx context.Context, vu *protocols.VU) protocols.Result {
	// Static fast path: the prebuilt URL/body; dynamic requests render their
	// fields from the next data row (and built-in generators) per request.
	reqURL, reqBodyStr := d.url, d.cfg.Body
	var headers map[string]string
	if d.dynamic {
		row := d.nextRow()
		reqURL = appendParams(vars.Interp(d.cfg.URL, row), d.cfg.Params, row)
		reqBodyStr = vars.Interp(d.cfg.Body, row)
		if len(d.cfg.Headers) > 0 {
			headers = make(map[string]string, len(d.cfg.Headers))
			for k, v := range d.cfg.Headers {
				headers[vars.Interp(k, row)] = vars.Interp(v, row)
			}
		}
	} else {
		headers = d.cfg.Headers
	}

	res := protocols.Result{Group: d.group, Method: d.cfg.Method, URL: reqURL}

	var body io.Reader
	if reqBodyStr != "" {
		body = strings.NewReader(reqBodyStr)
	}
	req, err := http.NewRequestWithContext(ctx, d.cfg.Method, reqURL, body)
	if err != nil {
		res.ErrorKind = "build_request"
		return res
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// OTel: a fresh sampled W3C trace context per request so the target's APM
	// can correlate these calls (a header the user explicitly set still wins).
	if d.cfg.TraceHeader && req.Header.Get("traceparent") == "" {
		req.Header.Set("traceparent", newTraceparent())
	}
	res.SentBytes = int64(len(reqBodyStr))
	if reqBody := reqBodyStr; reqBody != "" {
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
