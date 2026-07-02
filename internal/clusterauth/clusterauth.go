// Package clusterauth secures the internal gRPC plane (apisrv/workerd →
// coordinatord) with a shared bearer token. When the token is empty the plane
// stays open (dev/back-compat); when set, the coordinator rejects any RPC —
// including StartRun/StopRun/StreamLive and worker registration — that does not
// present it, so reaching :7070 is no longer enough to control the cluster.
package clusterauth

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// perRPC attaches the bearer token to every outbound call.
type perRPC struct{ token string }

func (p perRPC) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + p.token}, nil
}

// The internal plane runs over plaintext on a trusted network; the token is the
// control, not TLS, so per-RPC creds are allowed without transport security.
func (perRPC) RequireTransportSecurity() bool { return false }

// DialOption returns a dial option that attaches the token, or a no-op when the
// token is empty.
func DialOption(token string) grpc.DialOption {
	if token == "" {
		return grpc.EmptyDialOption{}
	}
	return grpc.WithPerRPCCredentials(perRPC{token: token})
}

func present(ctx context.Context, token string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return false
	}
	got := strings.TrimPrefix(vals[0], "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// UnaryInterceptor enforces the token on unary RPCs (no-op when token is empty).
func UnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if token != "" && !present(ctx, token) {
			return nil, status.Error(codes.Unauthenticated, "invalid or missing cluster token")
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor enforces the token on streaming RPCs (no-op when empty).
func StreamInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if token != "" && !present(ss.Context(), token) {
			return status.Error(codes.Unauthenticated, "invalid or missing cluster token")
		}
		return handler(srv, ss)
	}
}
