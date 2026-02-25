package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestTokenValidator_Valid(t *testing.T) {
	v := NewTokenValidator("secret-token")

	if !v.Valid("secret-token") {
		t.Error("expected valid token to pass")
	}
	if v.Valid("wrong-token") {
		t.Error("expected wrong token to fail")
	}
	if v.Valid("") {
		t.Error("expected empty token to fail")
	}
}

func TestTokenValidator_ValidateHandshake(t *testing.T) {
	v := NewTokenValidator("abc")

	if !v.ValidateHandshake([]byte("abc")) {
		t.Error("expected exact match to pass")
	}
	if !v.ValidateHandshake([]byte("abcextra")) {
		t.Error("expected prefix match to pass")
	}
	if v.ValidateHandshake([]byte("ab")) {
		t.Error("expected short data to fail")
	}
	if v.ValidateHandshake([]byte("xyz")) {
		t.Error("expected wrong data to fail")
	}
}

func TestTokenValidator_HandshakeBytes(t *testing.T) {
	v := NewTokenValidator("test")
	b := v.HandshakeBytes()
	if string(b) != "test" {
		t.Errorf("expected 'test', got %q", string(b))
	}
	b[0] = 'X'
	if string(v.HandshakeBytes()) != "test" {
		t.Error("HandshakeBytes should return a copy")
	}
}

func TestHTTPMiddleware_HealthBypass(t *testing.T) {
	v := NewTokenValidator("secret")
	handler := v.HTTPMiddleware(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/healthz should bypass auth, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/readyz", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/readyz should bypass auth, got %d", w.Code)
	}
}

func TestHTTPMiddleware_MetricsUnprotected(t *testing.T) {
	v := NewTokenValidator("secret")
	handler := v.HTTPMiddleware(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/metrics unprotected should bypass auth, got %d", w.Code)
	}
}

func TestHTTPMiddleware_MetricsProtected(t *testing.T) {
	v := NewTokenValidator("secret")
	handler := v.HTTPMiddleware(true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/metrics protected without token should return 401, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("authorization", "Bearer secret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/metrics protected with token should return 200, got %d", w.Code)
	}
}

func TestHTTPMiddleware_StatusRequiresAuth(t *testing.T) {
	v := NewTokenValidator("secret")
	handler := v.HTTPMiddleware(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/status without token should return 401, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("authorization", "Bearer secret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/status with token should return 200, got %d", w.Code)
	}
}

func TestValidateGRPCContext(t *testing.T) {
	v := NewTokenValidator("grpc-secret")

	ctx := context.Background()
	if err := v.validateGRPCContext(ctx); err == nil {
		t.Error("expected error for missing metadata")
	}

	md := metadata.New(map[string]string{})
	ctx = metadata.NewIncomingContext(context.Background(), md)
	if err := v.validateGRPCContext(ctx); err == nil {
		t.Error("expected error for missing authorization")
	}

	md = metadata.New(map[string]string{"authorization": "Bearer wrong"})
	ctx = metadata.NewIncomingContext(context.Background(), md)
	if err := v.validateGRPCContext(ctx); err == nil {
		t.Error("expected error for wrong token")
	}

	md = metadata.New(map[string]string{"authorization": "Bearer grpc-secret"})
	ctx = metadata.NewIncomingContext(context.Background(), md)
	if err := v.validateGRPCContext(ctx); err != nil {
		t.Errorf("expected success for valid token, got %v", err)
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		input string
		token string
		ok    bool
	}{
		{"Bearer abc", "abc", true},
		{"Bearer ", "", false},
		{"bearer abc", "", false},
		{"abc", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		token, ok := extractBearerToken(tt.input)
		if token != tt.token || ok != tt.ok {
			t.Errorf("extractBearerToken(%q) = (%q, %v), want (%q, %v)", tt.input, token, ok, tt.token, tt.ok)
		}
	}
}

func TestAuthorizationHeader(t *testing.T) {
	v := NewTokenValidator("mytoken")
	if h := v.AuthorizationHeader(); h != "Bearer mytoken" {
		t.Errorf("expected 'Bearer mytoken', got %q", h)
	}
}
