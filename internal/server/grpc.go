package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/timestamppb"

	orbitv1 "github.com/daxroc/orbit/proto/orbit/v1"
)

type GRPCServer struct {
	orbitv1.UnimplementedOrbitServiceServer
	srv       *grpc.Server
	port      int
	podName   string
	validator *auth.TokenValidator

	mu             sync.RWMutex
	onRegister     func(ctx context.Context, reg *orbitv1.PeerRegistration) (*orbitv1.PeerRegistrationResponse, error)
	onSchedule     func(ctx context.Context, sched *orbitv1.ProbeSchedule) (*orbitv1.ProbeScheduleResponse, error)
	onStatusReport func(ctx context.Context, report *orbitv1.StatusReport) (*orbitv1.StatusReportResponse, error)
}

func NewGRPCServer(port int, podName string, validator *auth.TokenValidator) *GRPCServer {
	s := &GRPCServer{
		port:      port,
		podName:   podName,
		validator: validator,
	}

	s.srv = grpc.NewServer(
		grpc.UnaryInterceptor(validator.UnaryServerInterceptor()),
		grpc.StreamInterceptor(validator.StreamServerInterceptor()),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)

	orbitv1.RegisterOrbitServiceServer(s.srv, s)
	return s
}

func (s *GRPCServer) SetOnRegister(fn func(ctx context.Context, reg *orbitv1.PeerRegistration) (*orbitv1.PeerRegistrationResponse, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onRegister = fn
}

func (s *GRPCServer) SetOnSchedule(fn func(ctx context.Context, sched *orbitv1.ProbeSchedule) (*orbitv1.ProbeScheduleResponse, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSchedule = fn
}

func (s *GRPCServer) SetOnStatusReport(fn func(ctx context.Context, report *orbitv1.StatusReport) (*orbitv1.StatusReportResponse, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStatusReport = fn
}

func (s *GRPCServer) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	slog.Info("starting gRPC server", "port", s.port)
	if err := s.srv.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

func (s *GRPCServer) Stop() {
	slog.Info("stopping gRPC server")
	stopped := make(chan struct{})
	go func() {
		s.srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		slog.Warn("gRPC server GracefulStop timed out, forcing stop")
		s.srv.Stop()
	}
}

func (s *GRPCServer) Echo(_ context.Context, req *orbitv1.EchoRequest) (*orbitv1.EchoResponse, error) {
	return &orbitv1.EchoResponse{
		Payload:     req.Payload,
		SentAt:      req.SentAt,
		ReceivedAt:  timestamppb.Now(),
		ResponderId: s.podName,
	}, nil
}

// TODO: Register is intended for future use where peers explicitly register with
// the leader on startup. Currently, peer discovery is handled via the Kubernetes
// EndpointSlice API and this RPC is not invoked. Returns Accepted: false when
// no callback is configured.
func (s *GRPCServer) Register(ctx context.Context, req *orbitv1.PeerRegistration) (*orbitv1.PeerRegistrationResponse, error) {
	s.mu.RLock()
	fn := s.onRegister
	s.mu.RUnlock()

	if fn == nil {
		return &orbitv1.PeerRegistrationResponse{Accepted: false}, nil
	}
	return fn(ctx, req)
}

func (s *GRPCServer) AssignSchedule(ctx context.Context, req *orbitv1.ProbeSchedule) (*orbitv1.ProbeScheduleResponse, error) {
	s.mu.RLock()
	fn := s.onSchedule
	s.mu.RUnlock()

	if fn == nil {
		return &orbitv1.ProbeScheduleResponse{Accepted: false, Error: "not ready"}, nil
	}
	return fn(ctx, req)
}

// TODO: ReportStatus is intended for future use where peers push status reports
// to the leader (active flows, bytes transferred, etc.) for central aggregation.
// Currently not invoked by any generator. Returns Acknowledged: false when no
// callback is configured.
func (s *GRPCServer) ReportStatus(ctx context.Context, req *orbitv1.StatusReport) (*orbitv1.StatusReportResponse, error) {
	s.mu.RLock()
	fn := s.onStatusReport
	s.mu.RUnlock()

	if fn == nil {
		return &orbitv1.StatusReportResponse{Acknowledged: false}, nil
	}
	return fn(ctx, req)
}

// TODO: StreamData is intended for bidirectional payload streaming between peers
// (e.g. large payload echo or sustained gRPC bandwidth tests). No generator
// currently invokes this RPC; it echoes received chunks back as a placeholder.
func (s *GRPCServer) StreamData(stream orbitv1.OrbitService_StreamDataServer) error {
	for {
		chunk, err := stream.Recv()
		if err != nil {
			return err
		}
		resp := &orbitv1.DataChunk{
			FlowId:   chunk.FlowId,
			Payload:  chunk.Payload,
			Sequence: chunk.Sequence,
			SentAt:   timestamppb.Now(),
			Checksum: chunk.Checksum,
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}
