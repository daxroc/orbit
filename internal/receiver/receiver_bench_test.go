package receiver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/checksum"
	"github.com/daxroc/orbit/internal/recorder"
)

func newTestHTTPReceiver() (*HTTPReceiver, *auth.TokenValidator) {
	v := auth.NewTokenValidator("bench-token")
	rec := recorder.NewAppRecorder()
	return NewHTTPReceiver(0, v, rec), v
}

// BenchmarkHTTPEchoHandler benchmarks the /echo handler at various payload sizes.
// This is the hot path for satellite HTTP traffic.
func BenchmarkHTTPEchoHandler(b *testing.B) {
	for _, size := range []int{512, 4096, 65536} {
		b.Run(fmt.Sprintf("payload_%d", size), func(b *testing.B) {
			r, v := newTestHTTPReceiver()

			payload := make([]byte, size)
			rand.Read(payload)
			cs := checksum.ComputeHex(payload)

			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(payload))
				req.Header.Set("Authorization", v.AuthorizationHeader())
				req.Header.Set("X-Orbit-Flow-ID", "bench-flow")
				req.Header.Set("X-Orbit-Checksum", cs)
				w := httptest.NewRecorder()
				r.handleEcho(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("unexpected status %d", w.Code)
				}
			}
		})
	}
}

// BenchmarkHTTPEchoE2E benchmarks the full HTTP stack (net/http server → handler → response)
// to capture middleware, auth, and serialization overhead.
func BenchmarkHTTPEchoE2E(b *testing.B) {
	for _, size := range []int{512, 4096} {
		b.Run(fmt.Sprintf("payload_%d", size), func(b *testing.B) {
			r, v := newTestHTTPReceiver()
			ts := httptest.NewServer(v.HTTPMiddleware(false, http.HandlerFunc(r.handleEcho)))
			defer ts.Close()

			payload := make([]byte, size)
			rand.Read(payload)
			cs := checksum.ComputeHex(payload)

			client := ts.Client()

			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				req, _ := http.NewRequest(http.MethodPost, ts.URL+"/echo", bytes.NewReader(payload))
				req.Header.Set("Authorization", v.AuthorizationHeader())
				req.Header.Set("X-Orbit-Flow-ID", "bench-flow")
				req.Header.Set("X-Orbit-Checksum", cs)
				resp, err := client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		})
	}
}

// BenchmarkHTTPEchoParallel benchmarks the /echo handler under concurrent load
// using keep-alive connections to avoid ephemeral port exhaustion.
func BenchmarkHTTPEchoParallel(b *testing.B) {
	for _, size := range []int{512, 4096} {
		b.Run(fmt.Sprintf("payload_%d", size), func(b *testing.B) {
			r, v := newTestHTTPReceiver()
			ts := httptest.NewServer(v.HTTPMiddleware(false, http.HandlerFunc(r.handleEcho)))
			defer ts.Close()

			payload := make([]byte, size)
			rand.Read(payload)
			cs := checksum.ComputeHex(payload)

			transport := &http.Transport{
				MaxIdleConnsPerHost: 256,
				DisableKeepAlives:   false,
			}
			client := &http.Client{Transport: transport}

			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					req, _ := http.NewRequest(http.MethodPost, ts.URL+"/echo", bytes.NewReader(payload))
					req.Header.Set("Authorization", v.AuthorizationHeader())
					req.Header.Set("X-Orbit-Flow-ID", "bench-flow")
					req.Header.Set("X-Orbit-Checksum", cs)
					resp, err := client.Do(req)
					if err != nil {
						b.Fatal(err)
					}
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			})
		})
	}
}

// BenchmarkTCPReceiverThroughput benchmarks the TCP receiver read loop.
func BenchmarkTCPReceiverThroughput(b *testing.B) {
	for _, size := range []int{1400, 32768} {
		b.Run(fmt.Sprintf("payload_%d", size), func(b *testing.B) {
			v := auth.NewTokenValidator("bench-token")
			rec := recorder.NewAppRecorder()
			tr := NewTCPReceiver(0, v, rec)

			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				b.Fatal(err)
			}
			tr.listener = ln
			addr := ln.Addr().String()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					go tr.handleConn(ctx, conn)
				}
			}()

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()

			token := v.HandshakeBytes()
			conn.Write(token)

			payload := make([]byte, size)
			rand.Read(payload)

			// drain ACK bytes from receiver
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()

			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := conn.Write(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkChecksumCompute benchmarks SHA-256 checksum at typical payload sizes.
func BenchmarkChecksumCompute(b *testing.B) {
	for _, size := range []int{512, 4096, 65536} {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			data := make([]byte, size)
			rand.Read(data)
			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				checksum.Compute(data)
			}
		})
	}
}

// BenchmarkChecksumVerify benchmarks checksum verification (compute + compare).
func BenchmarkChecksumVerify(b *testing.B) {
	for _, size := range []int{512, 4096} {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			data := make([]byte, size)
			rand.Read(data)
			expected := checksum.Compute(data)
			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				checksum.Verify(data, expected)
			}
		})
	}
}

// BenchmarkChecksumComputeHex benchmarks hex-encoded checksum (used in HTTP headers).
func BenchmarkChecksumComputeHex(b *testing.B) {
	for _, size := range []int{512, 4096} {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			data := make([]byte, size)
			rand.Read(data)
			b.SetBytes(int64(size))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				checksum.ComputeHex(data)
			}
		})
	}
}

// BenchmarkChecksumHexDecode benchmarks hex decoding of incoming checksum header.
func BenchmarkChecksumHexDecode(b *testing.B) {
	data := make([]byte, 4096)
	rand.Read(data)
	hexStr := checksum.ComputeHex(data)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hex.DecodeString(hexStr)
	}
}

// BenchmarkAuthValidateHandshake benchmarks TCP/UDP handshake token validation.
func BenchmarkAuthValidateHandshake(b *testing.B) {
	v := auth.NewTokenValidator("testing123")
	token := v.HandshakeBytes()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v.ValidateHandshake(token)
	}
}

// BenchmarkAuthValidBearerToken benchmarks HTTP bearer token validation.
func BenchmarkAuthValidBearerToken(b *testing.B) {
	v := auth.NewTokenValidator("testing123")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v.Valid("testing123")
	}
}

// BenchmarkNetutilStripPort benchmarks the per-request StripPort call.
func BenchmarkNetutilStripPort(b *testing.B) {
	addr := "172.0.6.39:60982"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		net.SplitHostPort(addr)
	}
}
