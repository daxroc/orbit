package receiver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
)

type UDPReceiver struct {
	port      int
	validator *auth.TokenValidator
	recorder  *recorder.AppRecorder

	mu     sync.Mutex
	cancel context.CancelFunc
	conn   *net.UDPConn
}

func NewUDPReceiver(port int, validator *auth.TokenValidator, rec *recorder.AppRecorder) *UDPReceiver {
	return &UDPReceiver{
		port:      port,
		validator: validator,
		recorder:  rec,
	}
}

func (r *UDPReceiver) Type() string { return "udp" }

func (r *UDPReceiver) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", r.port))
	if err != nil {
		cancel()
		return err
	}

	r.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		cancel()
		return fmt.Errorf("udp receiver listen: %w", err)
	}

	slog.Info("UDP receiver listening", "port", r.port)

	go func() {
		<-ctx.Done()
		r.conn.Close()
	}()

	tokenLen := len(r.validator.HandshakeBytes())
	buf := make([]byte, 65535)

	for {
		n, remoteAddr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("udp read error", "error", err)
			continue
		}

		if n < tokenLen || !r.validator.ValidateHandshake(buf[:tokenLen]) {
			slog.Debug("udp packet rejected: invalid token", "remote", remoteAddr)
			continue
		}

		r.recorder.AddBytesReceived(int64(n))
		r.recorder.AddPacketsReceived(1)
		metrics.ReceiverBytes.WithLabelValues("udp").Add(float64(n))

		source := stripPort(remoteAddr.String())
		target := stripPort(r.conn.LocalAddr().String())
		metrics.AppBytesReceived.WithLabelValues(
			"", "", "udp-stream", "udp", source, target, "east-west",
		).Add(float64(n))
		metrics.AppPacketsReceived.WithLabelValues(
			"", "", "udp-stream", "udp", source, target,
		).Inc()
	}
}

func (r *UDPReceiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}
