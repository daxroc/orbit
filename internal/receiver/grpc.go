package receiver

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/checksum"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	orbitv1 "github.com/daxroc/orbit/proto/orbit/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCReceiver struct {
	port      int
	validator *auth.TokenValidator
	appRec    *recorder.AppRecorder
	srv       *grpc.Server
}

func NewGRPCReceiver(port int, validator *auth.TokenValidator, appRec *recorder.AppRecorder) *GRPCReceiver {
	return &GRPCReceiver{
		port:      port,
		validator: validator,
		appRec:    appRec,
	}
}

func (r *GRPCReceiver) Name() string { return "grpc" }

func (r *GRPCReceiver) Start(_ context.Context) error {
	r.srv = grpc.NewServer(
		grpc.UnaryInterceptor(r.validator.UnaryServerInterceptor()),
		grpc.StreamInterceptor(r.validator.StreamServerInterceptor()),
	)
	orbitv1.RegisterOrbitServiceServer(r.srv, &grpcEchoServer{appRec: r.appRec})

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", r.port))
	if err != nil {
		return fmt.Errorf("grpc receiver listen: %w", err)
	}

	slog.Info("starting gRPC receiver", "port", r.port)
	go func() {
		if err := r.srv.Serve(lis); err != nil {
			slog.Error("gRPC receiver serve error", "error", err)
		}
	}()
	return nil
}

func (r *GRPCReceiver) Stop() {
	if r.srv != nil {
		r.srv.GracefulStop()
	}
}

type grpcEchoServer struct {
	orbitv1.UnimplementedOrbitServiceServer
	appRec *recorder.AppRecorder
}

func (s *grpcEchoServer) Echo(_ context.Context, req *orbitv1.EchoRequest) (*orbitv1.EchoResponse, error) {
	s.appRec.AddBytesReceived(int64(len(req.Payload)))
	metrics.ReceiverBytes.WithLabelValues("grpc").Add(float64(len(req.Payload)))
	metrics.ReceiverConnections.WithLabelValues("grpc").Inc()
	resp := &orbitv1.EchoResponse{
		Payload:    req.Payload,
		SentAt:     req.SentAt,
		ReceivedAt: timestamppb.Now(),
	}
	s.appRec.AddBytesSent(int64(len(resp.Payload)))
	return resp, nil
}

func (s *grpcEchoServer) StreamData(stream orbitv1.OrbitService_StreamDataServer) error {
	for {
		chunk, err := stream.Recv()
		if err != nil {
			return err
		}
		s.appRec.AddBytesReceived(int64(len(chunk.Payload)))
		s.appRec.AddPacketsReceived(1)
		metrics.ReceiverBytes.WithLabelValues("grpc").Add(float64(len(chunk.Payload)))

		if len(chunk.Checksum) > 0 && !checksum.Verify(chunk.Payload, chunk.Checksum) {
			metrics.ChecksumErrors.WithLabelValues("grpc", "grpc", "", chunk.FlowId).Inc()
			slog.Warn("checksum mismatch in stream", "flow_id", chunk.FlowId, "seq", chunk.Sequence)
		}

		resp := &orbitv1.DataChunk{
			FlowId:   chunk.FlowId,
			Payload:  chunk.Payload,
			Sequence: chunk.Sequence,
			SentAt:   timestamppb.Now(),
			Checksum: checksum.Compute(chunk.Payload),
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
		s.appRec.AddBytesSent(int64(len(resp.Payload)))
		s.appRec.AddPacketsSent(1)
	}
}
