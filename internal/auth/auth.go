package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	HeaderAuthorization = "authorization"
	BearerPrefix        = "Bearer "
)

type TokenValidator struct {
	token []byte
}

func NewTokenValidator(token string) *TokenValidator {
	return &TokenValidator{token: []byte(token)}
}

func (v *TokenValidator) Valid(candidate string) bool {
	return subtle.ConstantTimeCompare(v.token, []byte(candidate)) == 1
}

func (v *TokenValidator) ValidateHandshake(data []byte) bool {
	if len(data) < len(v.token) {
		return false
	}
	return subtle.ConstantTimeCompare(v.token, data[:len(v.token)]) == 1
}

func (v *TokenValidator) HandshakeBytes() []byte {
	b := make([]byte, len(v.token))
	copy(b, v.token)
	return b
}

func (v *TokenValidator) HTTPMiddleware(protectMetrics bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if !protectMetrics && r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := extractBearerToken(r.Header.Get(HeaderAuthorization))
		if !ok || !v.Valid(token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (v *TokenValidator) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := v.validateGRPCContext(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func (v *TokenValidator) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := v.validateGRPCContext(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func (v *TokenValidator) GRPCDialOption() grpc.DialOption {
	return grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, HeaderAuthorization, BearerPrefix+string(v.token))
		return invoker(ctx, method, req, reply, cc, opts...)
	})
}

func (v *TokenValidator) GRPCStreamDialOption() grpc.DialOption {
	return grpc.WithStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, HeaderAuthorization, BearerPrefix+string(v.token))
		return streamer(ctx, desc, cc, method, opts...)
	})
}

func (v *TokenValidator) validateGRPCContext(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get(HeaderAuthorization)
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization header")
	}
	token, ok := extractBearerToken(values[0])
	if !ok || !v.Valid(token) {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

func extractBearerToken(header string) (string, bool) {
	if !strings.HasPrefix(header, BearerPrefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, BearerPrefix)
	if token == "" {
		return "", false
	}
	return token, true
}

func (v *TokenValidator) AuthorizationHeader() string {
	return fmt.Sprintf("%s%s", BearerPrefix, string(v.token))
}
