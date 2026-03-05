package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/config"
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

type PortConfig struct {
	GRPC             int
	HTTPEcho         int
	TCPReceiverStart int
	UDPReceiverStart int
}

type Coordinator struct {
	selfID    string
	validator *auth.TokenValidator
	ports     PortConfig

	mu           sync.RWMutex
	peers        map[string]*discovery.Peer
	assignments  map[string]*FlowAssignment
	active       bool
	runID        string
	scenarioName string

	nsAssignments  map[string][]*orbitv1.FlowSpec
	nsFlows        []*orbitv1.FlowSpec
	nsDist         config.NorthSouthDistribution
	rotationIndex  int
	lastRotation   time.Time
	rotateInterval time.Duration
	nsPeerSnapshot int
}

func New(selfID string, validator *auth.TokenValidator, ports PortConfig) *Coordinator {
	return &Coordinator{
		selfID:      selfID,
		validator:   validator,
		ports:       ports,
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

	c.scenarioName = scenarioName
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
				targetAddr := c.targetAddress(tmpl.Type, dstPeer)
				spec := templateToFlowSpec(tmpl, flowID, targetAddr, "east-west")
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

	startAt := timestamppb.New(time.Now().Add(5 * time.Second))

	for peerID, peer := range peers {
		assignment := assignments[peerID]
		var flows []*orbitv1.FlowSpec
		if assignment != nil {
			flows = assignment.Flows
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
		var nsFlows []*orbitv1.FlowSpec
		if c.nsAssignments != nil {
			nsFlows = c.nsAssignments[peerID]
		}

		schedule := &orbitv1.ProbeSchedule{
			ScenarioName:    scenarioName,
			RunId:           runID,
			EastWestFlows:   flows,
			NorthSouthFlows: nsFlows,
			StartAt:         startAt,
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
			slog.Info("schedule assigned", "peer", peerID, "flows", len(flows), "ns_flows", len(nsFlows))
		}
	}

	return nil
}

func (c *Coordinator) Heartbeat(ctx context.Context) error {
	c.mu.Lock()
	if len(c.nsFlows) > 0 {
		if len(c.peers) != c.nsPeerSnapshot {
			slog.Info("peer set changed, rebuilding NS assignments",
				"old_peers", c.nsPeerSnapshot,
				"new_peers", len(c.peers),
			)
			c.rebuildNSAssignments()
		} else if c.rotateInterval > 0 && time.Since(c.lastRotation) >= c.rotateInterval {
			c.rotateNSPeers()
		}
	}
	scenarioName := c.scenarioName
	assignments := c.assignments
	c.mu.Unlock()
	return c.DistributeSchedule(ctx, scenarioName, assignments)
}

func (c *Coordinator) ClearAndDistribute(ctx context.Context) error {
	c.mu.Lock()
	c.scenarioName = ""
	c.runID = fmt.Sprintf("clear-%d", time.Now().UnixMilli())
	c.assignments = make(map[string]*FlowAssignment)
	c.nsAssignments = make(map[string][]*orbitv1.FlowSpec)
	c.nsFlows = nil
	c.mu.Unlock()
	return c.DistributeSchedule(ctx, "", make(map[string]*FlowAssignment))
}

func (c *Coordinator) BuildNorthSouthAssignments(dist config.NorthSouthDistribution, nsFlows []*orbitv1.FlowSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nsFlows = nsFlows
	c.nsDist = dist
	c.nsAssignments = make(map[string][]*orbitv1.FlowSpec)
	c.rotationIndex = 0
	c.lastRotation = time.Now()

	if ri := dist.RotateInterval; ri != "" {
		if d, err := time.ParseDuration(ri); err == nil && d > 0 {
			c.rotateInterval = d
		}
	} else {
		c.rotateInterval = 0
	}

	if len(nsFlows) == 0 {
		return
	}

	c.rebuildNSAssignments()
}

func (c *Coordinator) rebuildNSAssignments() {
	c.nsAssignments = make(map[string][]*orbitv1.FlowSpec)
	c.nsPeerSnapshot = len(c.peers)

	if len(c.nsFlows) == 0 {
		return
	}

	selected := c.selectNSPeers()
	for _, pid := range selected {
		c.nsAssignments[pid] = c.nsFlows
	}

	slog.Info("north-south distribution built",
		"mode", c.nsDist.EffectiveMode(),
		"total_peers", len(c.peers),
		"selected_peers", len(selected),
		"ns_flows", len(c.nsFlows),
	)
}

func (c *Coordinator) selectNSPeers() []string {
	peerIDs := c.sortedPeerIDs()
	if len(peerIDs) == 0 {
		return nil
	}

	switch c.nsDist.EffectiveMode() {
	case config.NSDistModeAll:
		return peerIDs
	case config.NSDistModePercentage:
		pct := c.nsDist.Percentage
		if pct <= 0 {
			pct = 25
		}
		if pct > 100 {
			pct = 100
		}
		count := int(math.Ceil(float64(len(peerIDs)) * float64(pct) / 100.0))
		if count < 1 {
			count = 1
		}
		start := c.rotationIndex % len(peerIDs)
		selected := make([]string, 0, count)
		for i := 0; i < count; i++ {
			idx := (start + i) % len(peerIDs)
			selected = append(selected, peerIDs[idx])
		}
		return selected
	default:
		return []string{c.selfID}
	}
}

func (c *Coordinator) sortedPeerIDs() []string {
	ids := make([]string, 0, len(c.peers))
	for id := range c.peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *Coordinator) rotateNSPeers() {
	peerIDs := c.sortedPeerIDs()
	if len(peerIDs) == 0 {
		return
	}

	c.rotationIndex++
	c.lastRotation = time.Now()
	c.nsPeerSnapshot = len(c.peers)
	c.nsAssignments = make(map[string][]*orbitv1.FlowSpec)

	selected := c.selectNSPeers()
	for _, pid := range selected {
		c.nsAssignments[pid] = c.nsFlows
	}

	slog.Info("north-south peers rotated",
		"rotation_index", c.rotationIndex,
		"selected_peers", len(selected),
	)
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

func (c *Coordinator) targetAddress(flowType string, peer *discovery.Peer) string {
	switch flowType {
	case "http":
		return fmt.Sprintf("%s:%d", peer.IP, c.ports.HTTPEcho)
	case "grpc":
		return fmt.Sprintf("%s:%d", peer.IP, c.ports.GRPC)
	case "tcp-stream", "connection-churn":
		return fmt.Sprintf("%s:%d", peer.IP, c.ports.TCPReceiverStart)
	case "udp-stream":
		return fmt.Sprintf("%s:%d", peer.IP, c.ports.UDPReceiverStart)
	case "icmp":
		return peer.IP
	default:
		return peer.Address
	}
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
