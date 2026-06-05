// Package httpd implements the HTTP/HTTPS load-generation Driver.
package httpd

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

func init() {
	protocols.Register(loadifyv1.Protocol_PROTOCOL_HTTP, factory)
	protocols.Register(loadifyv1.Protocol_PROTOCOL_HTTPS, factory)
}

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
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // load tester targets arbitrary endpoints
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
	res := protocols.Result{Group: d.group}

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

	var dnsStart, connStart, tlsStart, firstByte time.Time
	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { res.DNSUs = sinceUs(dnsStart) },
		ConnectStart:         func(_, _ string) { connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { res.ConnectUs = sinceUs(connStart) },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { res.TLSUs = sinceUs(tlsStart) },
		GotFirstResponseByte: func() { firstByte = time.Now() },
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
	n, _ := io.Copy(io.Discard, resp.Body)
	res.LatencyUs = sinceUs(start)
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
	return res
}

// Teardown closes idle connections.
func (d *Driver) Teardown(_ context.Context) error {
	if d.client != nil {
		d.client.CloseIdleConnections()
	}
	return nil
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
