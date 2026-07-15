package sampler

import (
	"testing"
	"time"
	"unicode/utf8"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/worker/protocols"
	"google.golang.org/protobuf/proto"
)

func TestFlushCapsAndResetsSamples(t *testing.T) {
	s := New("run", "worker", loadifyv1.Protocol_PROTOCOL_HTTP)

	// Far more than the caps of both errors and successes.
	for i := 0; i < 500; i++ {
		s.Record(protocols.Result{Group: "g", OK: true, Status: 200, LatencyUs: 1000})
	}
	for i := 0; i < 500; i++ {
		s.Record(protocols.Result{Group: "g", OK: false, Status: 500, ErrorKind: "http_status", LatencyUs: 2000})
	}

	batch := s.Flush(time.Now())
	if got := len(batch.Samples); got != okSampleCap+errSampleCap {
		t.Fatalf("samples = %d, want %d", got, okSampleCap+errSampleCap)
	}
	var errs int
	for _, sm := range batch.Samples {
		if !sm.Ok {
			errs++
		}
	}
	if errs != errSampleCap {
		t.Errorf("error samples = %d, want %d", errs, errSampleCap)
	}

	// After flush the sample buffer and counters reset.
	s.Record(protocols.Result{Group: "g", OK: true, Status: 200, LatencyUs: 1000})
	batch2 := s.Flush(time.Now())
	if len(batch2.Samples) != 1 {
		t.Errorf("post-reset samples = %d, want 1", len(batch2.Samples))
	}
}

// TestFlushMarshalsWithInvalidUTF8Body is the regression for the production
// incident: a body captured with invalid UTF-8 (a Chinese request body
// truncated mid-rune at the byte cap, or a binary/gzip response) must not make
// the batch un-marshalable. protobuf rejects a string field that isn't valid
// UTF-8, and that error ("string field contains invalid UTF-8") failed every
// metric send, wedging the worker in an endless reconnect loop with zero data.
func TestFlushMarshalsWithInvalidUTF8Body(t *testing.T) {
	s := New("run", "worker", loadifyv1.Protocol_PROTOCOL_HTTP)

	// "测" is 3 bytes; taking 2 leaves a dangling partial rune — exactly what a
	// byte-cap truncation of a Chinese body produces. Plus a raw binary response.
	partialRune := "棱镜压测" + string([]byte("测")[:2])
	binaryResp := string([]byte{0xff, 0xfe, 0x00, 0x9c, 0x80})

	s.Record(protocols.Result{
		Group:     "g",
		Method:    "POST",
		URL:       "http://api/v1/messages",
		Status:    200,
		OK:        false,
		ErrorKind: "x",
		ReqBody:   partialRune,
		RespBody:  binaryResp,
		LatencyUs: 1000,
	})

	batch := s.Flush(time.Now())
	if _, err := proto.Marshal(batch); err != nil {
		t.Fatalf("batch failed to marshal (this is what wedged the worker): %v", err)
	}
	if len(batch.Samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(batch.Samples))
	}
	sm := batch.Samples[0]
	if !utf8.ValidString(sm.ReqBody) {
		t.Errorf("ReqBody is not valid UTF-8: %q", sm.ReqBody)
	}
	if !utf8.ValidString(sm.RespBody) {
		t.Errorf("RespBody is not valid UTF-8: %q", sm.RespBody)
	}
}

// TestSampleCarriesRequestAndBody ensures the live-log fields (method, URL,
// response body snippet) survive the Result -> Sample conversion.
func TestSampleCarriesRequestAndBody(t *testing.T) {
	s := New("run", "worker", loadifyv1.Protocol_PROTOCOL_HTTP)
	s.Record(protocols.Result{
		Group:    "g",
		Method:   "POST",
		URL:      "http://api/login",
		Status:   401,
		RespBody: `{"error":"bad credentials"}`,
	})
	batch := s.Flush(time.Now())
	if len(batch.Samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(batch.Samples))
	}
	sm := batch.Samples[0]
	if sm.Method != "POST" || sm.Url != "http://api/login" || sm.RespBody != `{"error":"bad credentials"}` {
		t.Errorf("sample = method %q url %q body %q", sm.Method, sm.Url, sm.RespBody)
	}
}
