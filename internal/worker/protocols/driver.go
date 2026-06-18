// Package protocols defines the Driver abstraction that every load-generation
// protocol (HTTP, gRPC, WebSocket, SSE) implements, plus the shared result type.
package protocols

import (
	"context"
	"fmt"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
)

// Result is a single iteration's observation, fed into the metrics recorder.
type Result struct {
	Group     string
	Method    string // request verb/operation (GET, gRPC method, ...)
	URL       string // request target
	Status    int32
	OK        bool
	ErrorKind string
	LatencyUs int64
	DNSUs     int64
	ConnectUs int64
	TLSUs     int64
	TTFBUs    int64
	SentBytes int64
	RecvBytes int64
	ReqBody   string // truncated request body snippet (what was actually sent)
	RespBody  string // truncated response body snippet for the live log
}

// RespBodyCap bounds the response body snippet captured per iteration so the
// live log stays cheap regardless of payload size.
const RespBodyCap = 1024

// VU carries per-virtual-user state passed to Exec on every iteration.
type VU struct {
	ID        int
	Iteration int64
}

// Driver drives one protocol for the lifetime of a run. Prepare sets up shared
// resources (connection pools); Exec performs one iteration and returns a
// Result; Teardown releases resources. Exec must be safe for concurrent use by
// many VUs.
type Driver interface {
	Prepare(ctx context.Context) error
	Exec(ctx context.Context, vu *VU) Result
	Teardown(ctx context.Context) error
}

// MultiDriver is an optional Driver extension that emits several results per
// iteration — one per labeled step plus a transaction total — so a scenario's
// per-interface metrics and end-to-end transaction timing are captured. The
// executor uses ExecMulti when available and records every result.
type MultiDriver interface {
	Driver
	ExecMulti(ctx context.Context, vu *VU) []Result
}

// Factory builds a Driver from a parsed plan.
type Factory func(p *plan.Plan) (Driver, error)

var registry = map[loadifyv1.Protocol]Factory{}

// Register associates a protocol with its Driver factory.
func Register(proto loadifyv1.Protocol, f Factory) {
	registry[proto] = f
}

// New builds the Driver for the plan's protocol.
func New(proto loadifyv1.Protocol, p *plan.Plan) (Driver, error) {
	f, ok := registry[proto]
	if !ok {
		return nil, fmt.Errorf("protocols: no driver registered for %s", proto)
	}
	return f(p)
}
