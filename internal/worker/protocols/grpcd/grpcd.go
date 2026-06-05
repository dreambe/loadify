// Package grpcd implements the gRPC load-generation Driver. It invokes a unary
// method dynamically: the method is resolved either from a user-supplied
// FileDescriptorSet or from the process's global proto registry, and the
// request is built from JSON, so no generated stubs for the target are needed.
package grpcd

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func init() {
	protocols.Register(loadifyv1.Protocol_PROTOCOL_GRPC, factory)
}

func factory(p *plan.Plan) (protocols.Driver, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Driver{cfg: p.GRPC}, nil
}

// Driver invokes a single unary gRPC method under load. The resolved method
// descriptor and a pre-marshalled request template are shared read-only across
// VUs; each Exec decodes a fresh request/response pair so concurrent calls
// never share mutable proto state.
type Driver struct {
	cfg     *plan.GRPCConfig
	group   string
	timeout time.Duration

	conn       *grpc.ClientConn
	method     protoreflect.MethodDescriptor
	grpcMethod string // canonical "/pkg.Svc/Method"
	reqBytes   []byte
	md         metadata.MD
}

// Prepare resolves the target method and opens the shared client connection.
func (d *Driver) Prepare(_ context.Context) error {
	d.group = d.cfg.Group
	if d.group == "" {
		d.group = "default"
	}
	d.timeout = time.Duration(d.cfg.TimeoutMs) * time.Millisecond
	if d.timeout == 0 {
		d.timeout = 30 * time.Second
	}

	files, err := d.resolverFiles()
	if err != nil {
		return err
	}
	method, canonical, err := resolveMethod(files, d.cfg.FullMethod)
	if err != nil {
		return err
	}
	d.method = method
	d.grpcMethod = canonical

	// Build the request template once, then store its wire bytes so each
	// iteration can cheaply decode an independent copy.
	in := dynamicpb.NewMessage(method.Input())
	reqJSON := d.cfg.RequestJSON
	if strings.TrimSpace(reqJSON) == "" {
		reqJSON = "{}"
	}
	if err := (protojson.UnmarshalOptions{Resolver: dynamicpb.NewTypes(files)}).Unmarshal([]byte(reqJSON), in); err != nil {
		return fmt.Errorf("grpcd: decode request_json: %w", err)
	}
	d.reqBytes, err = proto.Marshal(in)
	if err != nil {
		return fmt.Errorf("grpcd: marshal request: %w", err)
	}

	if len(d.cfg.Metadata) > 0 {
		d.md = metadata.New(d.cfg.Metadata)
	}

	creds := credentials.TransportCredentials(insecure.NewCredentials())
	if !d.cfg.PlaintextProbe {
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // load tester targets arbitrary endpoints
	}
	conn, err := grpc.NewClient(d.cfg.Target,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<20)),
	)
	if err != nil {
		return fmt.Errorf("grpcd: dial %s: %w", d.cfg.Target, err)
	}
	d.conn = conn
	return nil
}

// Exec performs one unary call and records latency and status.
func (d *Driver) Exec(ctx context.Context, _ *protocols.VU) protocols.Result {
	res := protocols.Result{Group: d.group}

	in := dynamicpb.NewMessage(d.method.Input())
	if err := proto.Unmarshal(d.reqBytes, in); err != nil {
		res.ErrorKind = "build_request"
		return res
	}
	out := dynamicpb.NewMessage(d.method.Output())
	res.SentBytes = int64(len(d.reqBytes))

	opCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	if d.md != nil {
		opCtx = metadata.NewOutgoingContext(opCtx, d.md)
	}

	start := time.Now()
	err := d.conn.Invoke(opCtx, d.grpcMethod, in, out)
	res.LatencyUs = time.Since(start).Microseconds()
	if err != nil {
		st := status.Convert(err)
		res.Status = int32(st.Code())
		res.ErrorKind = "grpc_" + st.Code().String()
		return res
	}
	res.TTFBUs = res.LatencyUs
	res.RecvBytes = int64(proto.Size(out))
	res.OK = true
	return res
}

// Teardown closes the shared connection.
func (d *Driver) Teardown(_ context.Context) error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}

// resolverFiles returns the descriptor source: the user's FileDescriptorSet if
// provided, otherwise the process-global registry (covers reflection-free
// standard services such as grpc.health.v1).
func (d *Driver) resolverFiles() (*protoregistry.Files, error) {
	if len(d.cfg.DescriptorSet) == 0 {
		return protoregistry.GlobalFiles, nil
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(d.cfg.DescriptorSet, &fds); err != nil {
		return nil, fmt.Errorf("grpcd: invalid descriptor_set: %w", err)
	}
	files, err := protodesc.NewFiles(&fds)
	if err != nil {
		return nil, fmt.Errorf("grpcd: build descriptors: %w", err)
	}
	return files, nil
}

// resolveMethod parses a "/pkg.Svc/Method" (or "pkg.Svc/Method") string and
// looks up the method descriptor, returning the canonical leading-slash form
// that grpc.ClientConn.Invoke expects.
func resolveMethod(files interface {
	FindDescriptorByName(protoreflect.FullName) (protoreflect.Descriptor, error)
}, fullMethod string) (protoreflect.MethodDescriptor, string, error) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash <= 0 || slash == len(trimmed)-1 {
		return nil, "", fmt.Errorf("grpcd: full_method must be /pkg.Service/Method, got %q", fullMethod)
	}
	svcName := protoreflect.FullName(trimmed[:slash])
	methodName := protoreflect.Name(trimmed[slash+1:])

	desc, err := files.FindDescriptorByName(svcName)
	if err != nil {
		return nil, "", fmt.Errorf("grpcd: service %q not found: %w", svcName, err)
	}
	svc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, "", fmt.Errorf("grpcd: %q is not a service", svcName)
	}
	method := svc.Methods().ByName(methodName)
	if method == nil {
		return nil, "", fmt.Errorf("grpcd: method %q not found on %q", methodName, svcName)
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return nil, "", fmt.Errorf("grpcd: %s is streaming; only unary methods are supported", fullMethod)
	}
	return method, "/" + string(svcName) + "/" + string(methodName), nil
}
