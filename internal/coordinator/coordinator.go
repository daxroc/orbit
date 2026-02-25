package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/discovery"
	orbitv1 "github.com/daxroc/orbit/proto/orbit/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type FlowAssignment struct {
	PeerID string
	Flows  []*orbitv1.FlowSpec
}

type Coordinator struct {
	selfID    string
	validator *auth.TokenValidator

	mu          sync.RWMutex
	peers       map[string]*discovery.Peer
	assignments map[string]*FlowAssignment
	active      bool
	runID       string
}

func New(selfID string, validator *auth.TokenValidator) *Coordinator {
	return &Coordinator{
		selfID:      selfID,
		validator:   validator,
		peers:       make(map[string]*discovery.Peer),
		assignments: make(map[string]*FlowAssignment),
	}
}

func (c *Coordinator) IsActive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

func (c *Coordinator) SetActive(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = active
}

func (c *Coordinator) UpdatePeers(peers map[string]*discovery.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peers = peers
}

func (c *Coordinator) BuildMeshAssignments(scenarioName string, eastWestFlows []FlowTemplate) map[string]*FlowAssignment {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.runID = fmt.Sprintf("%s-%d", scenarioName, time.Now().UnixMilli())
	assignments := make(map[string]*FlowAssignment)

	peerIDs := make([]string, 0, len(c.peers))
	for id := range c.peers {
		peerIDs = append(peerIDs, id)
	}

	if len(peerIDs) == 0 {
		return assignments
	}

	flowID := 0
	for _, tmpl := range eastWestFlows {
		for i, srcID := range peerIDs {
			for j, dstID := range peerIDs {
				if i == j {
					continue
				}
				dstPeer := c.peers[dstID]
				spec := templateToFlowSpec(tmpl, flowID, dstPeer.Address, "east-west")
				flowID++

				if _, ok := assignments[srcID]; !ok {
					assignments[srcID] = &FlowAssignment{PeerID: srcID}
				}
				assignments[srcID].Flows = append(assignments[srcID].Flows, spec)
			}
		}
	}

	c.assignments = assignments
	return assignments
}

func (c *Coordinator) DistributeSchedule(ctx context.Context, scenarioName string, assignments map[string]*FlowAssignment) error {
	c.mu.RLock()
	peers := c.peers
	runID := c.runID
	c.mu.RUnlock()

	for peerID, assignment := range assignments {
		peer, ok := peers[peerID]
		if !ok {
			slog.Warn("peer not found for assignment", "peer", peerID)
			continue
		}

		conn, err := grpc.NewClient(peer.Address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			c.validator.GRPCDialOption(),
			c.validator.GRPCStreamDialOption(),
		)
		if err != nil {
			slog.Error("failed to connect to peer", "peer", peerID, "error", err)
			continue
		}

		client := orbitv1.NewOrbitServiceClient(conn)
		schedule := &orbitv1.ProbeSchedule{
			ScenarioName:  scenarioName,
			RunId:         runID,
			EastWestFlows: assignment.Flows,
			StartAt:       timestamppb.New(time.Now().Add(5 * time.Second)),
		}

		resp, err := client.AssignSchedule(ctx, schedule)
		_ = conn.Close()

		if err != nil {
			slog.Error("failed to assign schedule", "peer", peerID, "error", err)
			continue
		}
		if !resp.Accepted {
			slog.Warn("peer rejected schedule", "peer", peerID, "error", resp.Error)
		} else {
			slog.Info("schedule assigned", "peer", peerID, "flows", len(assignment.Flows))
		}
	}

	return nil
}

type FlowTemplate struct {
	Type                 string
	BandwidthBps         int64
	PacketRate           int32
	PacketSize           int32
	RPS                  int32
	PayloadBytes         int32
	Connections          int32
	HTTPMethod           string
	KeepAlive            bool
	Pattern              string
	BurstDurationSeconds int32
	BurstIntervalSeconds int32
	ConnectionsPerSecond int32
	HoldDurationMs       int32
	DurationSeconds      int32
	IntervalMs           int32
}

func templateToFlowSpec(tmpl FlowTemplate, id int, targetAddr, direction string) *orbitv1.FlowSpec {
	return &orbitv1.FlowSpec{
		Id:                   fmt.Sprintf("flow-%d", id),
		Type:                 tmpl.Type,
		TargetAddress:        targetAddr,
		Direction:            direction,
		BandwidthBps:         tmpl.BandwidthBps,
		PacketRate:           tmpl.PacketRate,
		PacketSize:           tmpl.PacketSize,
		Rps:                  tmpl.RPS,
		PayloadBytes:         tmpl.PayloadBytes,
		Connections:          tmpl.Connections,
		HttpMethod:           tmpl.HTTPMethod,
		KeepAlive:            tmpl.KeepAlive,
		Pattern:              tmpl.Pattern,
		BurstDurationSeconds: tmpl.BurstDurationSeconds,
		BurstIntervalSeconds: tmpl.BurstIntervalSeconds,
		ConnectionsPerSecond: tmpl.ConnectionsPerSecond,
		HoldDurationMs:       tmpl.HoldDurationMs,
		Duration:             durationpb.New(time.Duration(tmpl.DurationSeconds) * time.Second),
		Interval:             durationpb.New(time.Duration(tmpl.IntervalMs) * time.Millisecond),
	}
}
