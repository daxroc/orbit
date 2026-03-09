package receiver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/netutil"
	"github.com/daxroc/orbit/internal/recorder"
)

type TCPReceiver struct {
	port      int
	validator *auth.TokenValidator
	recorder  *recorder.AppRecorder
	listener  net.Listener

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewTCPReceiver(port int, validator *auth.TokenValidator, rec *recorder.AppRecorder) *TCPReceiver {
	return &TCPReceiver{
		port:      port,
		validator: validator,
		recorder:  rec,
	}
}

func (r *TCPReceiver) Type() string { return "tcp" }

func (r *TCPReceiver) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	var err error
	r.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", r.port))
	if err != nil {
		cancel()
		return fmt.Errorf("tcp receiver listen: %w", err)
	}

	slog.Info("TCP receiver listening", "port", r.port)

	go func() {
		<-ctx.Done()
		r.listener.Close()
	}()

	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("tcp accept error", "error", err)
			continue
		}
		go r.handleConn(ctx, conn)
	}
}

func (r *TCPReceiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

func (r *TCPReceiver) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	tokenLen := len(r.validator.HandshakeBytes())
	handshake := make([]byte, tokenLen)
	if _, err := io.ReadFull(conn, handshake); err != nil {
		slog.Debug("tcp handshake read failed", "error", err)
		return
	}
	if !r.validator.ValidateHandshake(handshake) {
		slog.Warn("tcp handshake failed: invalid token", "remote", conn.RemoteAddr())
		return
	}

	r.recorder.AddConnection()
	defer r.recorder.RemoveConnection()
	metrics.ReceiverConnections.WithLabelValues("tcp").Inc()

	source := netutil.StripPort(conn.RemoteAddr().String())
	target := netutil.StripPort(conn.LocalAddr().String())
	labels := []string{"", "", "tcp-stream", "tcp", source, target}

	metrics.AppConnectionsActive.WithLabelValues(labels...).Inc()
	defer metrics.AppConnectionsActive.WithLabelValues(labels...).Dec()

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := conn.Read(buf)
		if n > 0 {
			r.recorder.AddBytesReceived(int64(n))
			metrics.AppBytesReceived.WithLabelValues(
				"", "", "tcp-stream", "tcp", source, target, "east-west",
			).Add(float64(n))
			metrics.ReceiverBytes.WithLabelValues("tcp").Add(float64(n))

			if _, werr := conn.Write([]byte{0}); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
