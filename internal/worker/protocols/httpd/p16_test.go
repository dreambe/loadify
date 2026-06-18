package httpd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// pemFor PEM-encodes a tls.Certificate's leaf cert + private key.
func pemFor(t *testing.T, cert tls.Certificate) (string, string) {
	t.Helper()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func run(t *testing.T, cfg *plan.HTTPConfig, vu *protocols.VU) protocols.Result {
	t.Helper()
	d := &Driver{cfg: cfg}
	if err := d.Prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return d.Exec(context.Background(), vu)
}

// TestFollowRedirects: default leaves the 302; FollowRedirects lands on 200.
func TestFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dest" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.Redirect(w, r, "/dest", http.StatusFound)
	}))
	defer srv.Close()

	if res := run(t, &plan.HTTPConfig{Method: "GET", URL: srv.URL + "/start"}, &protocols.VU{ID: 1}); res.Status != 302 {
		t.Errorf("no-follow status = %d, want 302", res.Status)
	}
	if res := run(t, &plan.HTTPConfig{Method: "GET", URL: srv.URL + "/start", FollowRedirects: true}, &protocols.VU{ID: 1}); res.Status != 200 {
		t.Errorf("follow status = %d, want 200", res.Status)
	}
}

// TestQueryParams: structured params are URL-encoded onto the request URL.
func TestQueryParams(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
	}))
	defer srv.Close()
	run(t, &plan.HTTPConfig{Method: "GET", URL: srv.URL + "/s", Params: []plan.ScenarioParam{{Key: "q", Value: "a b"}, {Key: "id", Value: "7"}}}, &protocols.VU{ID: 1})
	if gotQuery != "id=7&q=a+b" && gotQuery != "q=a+b&id=7" {
		t.Errorf("query = %q, want id=7 & q=a+b (encoded)", gotQuery)
	}
}

// TestCookieJarPerVU: a set cookie is echoed back on the same VU's next
// iteration, and a different VU does NOT carry it (isolated sessions).
func TestCookieJarPerVU(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sid"); err == nil {
			_, _ = w.Write([]byte("have:" + c.Value))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "VU1"})
		_, _ = w.Write([]byte("set"))
	}))
	defer srv.Close()

	d := &Driver{cfg: &plan.HTTPConfig{Method: "GET", URL: srv.URL + "/", CookieJar: true}}
	if err := d.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	vu1 := &protocols.VU{ID: 1}
	d.Exec(context.Background(), vu1)                        // sets cookie for VU1
	r2 := d.Exec(context.Background(), vu1)                  // VU1 should send it back
	r3 := d.Exec(context.Background(), &protocols.VU{ID: 2}) // VU2 has its own jar
	if r2.RespBody != "have:VU1" {
		t.Errorf("VU1 second iter body = %q, want have:VU1 (cookie persisted)", r2.RespBody)
	}
	if r3.RespBody != "set" {
		t.Errorf("VU2 body = %q, want set (isolated jar)", r3.RespBody)
	}
}

// TestMutualTLS: a client cert is presented when the server requires one.
func TestMutualTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	defer srv.Close()

	// Use the server's own cert/key as a client cert (any cert satisfies RequireAny).
	cert := srv.TLS.Certificates[0]
	certPEM, keyPEM := pemFor(t, cert)

	// Without a client cert → TLS handshake fails.
	if res := run(t, &plan.HTTPConfig{Method: "GET", URL: srv.URL, InsecureSkipVerify: true}, &protocols.VU{ID: 1}); res.ErrorKind == "" && res.Status == 200 {
		t.Errorf("expected failure without client cert, got status %d", res.Status)
	}
	// With the client cert → 200.
	var ok int32
	res := run(t, &plan.HTTPConfig{Method: "GET", URL: srv.URL, InsecureSkipVerify: true, ClientCertPEM: certPEM, ClientKeyPEM: keyPEM}, &protocols.VU{ID: 1})
	if res.Status == 200 {
		atomic.StoreInt32(&ok, 1)
	}
	if atomic.LoadInt32(&ok) != 1 {
		t.Errorf("mTLS status = %d kind=%q, want 200", res.Status, res.ErrorKind)
	}
}
