package coordinator

import (
	"testing"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/config"
	"github.com/daxroc/orbit/internal/discovery"
	orbitv1 "github.com/daxroc/orbit/proto/orbit/v1"
)

func newTestCoordinator(selfID string, peers map[string]*discovery.Peer) *Coordinator {
	c := New(selfID, auth.NewTokenValidator("test"), PortConfig{
		GRPC:             9090,
		HTTPEcho:         8080,
		TCPReceiverStart: 10000,
		UDPReceiverStart: 11000,
	})
	c.UpdatePeers(peers)
	return c
}

func testPeers(ids ...string) map[string]*discovery.Peer {
	peers := make(map[string]*discovery.Peer, len(ids))
	for _, id := range ids {
		peers[id] = &discovery.Peer{ID: id, IP: "10.0.0.1", Address: "10.0.0.1:9090"}
	}
	return peers
}

func testNSFlows(n int) []*orbitv1.FlowSpec {
	flows := make([]*orbitv1.FlowSpec, n)
	for i := range flows {
		flows[i] = &orbitv1.FlowSpec{Id: "ns-0", Type: "tcp-stream", TargetAddress: "1.2.3.4:10000", Direction: "north-south"}
	}
	return flows
}

func TestBuildNorthSouthAssignments_ModeOne(t *testing.T) {
	peers := testPeers("peer-a", "peer-b", "peer-c")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "one"}, testNSFlows(2))

	if len(c.nsAssignments) != 1 {
		t.Fatalf("expected 1 peer assigned, got %d", len(c.nsAssignments))
	}
	if _, ok := c.nsAssignments["peer-a"]; !ok {
		t.Fatal("expected self (peer-a) to be assigned NS flows in mode=one")
	}
}

func TestBuildNorthSouthAssignments_ModeDefault(t *testing.T) {
	peers := testPeers("peer-a", "peer-b")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{}, testNSFlows(1))

	if len(c.nsAssignments) != 1 {
		t.Fatalf("expected 1 peer (default=one), got %d", len(c.nsAssignments))
	}
}

func TestBuildNorthSouthAssignments_ModeAll(t *testing.T) {
	peers := testPeers("peer-a", "peer-b", "peer-c")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "all"}, testNSFlows(2))

	if len(c.nsAssignments) != 3 {
		t.Fatalf("expected all 3 peers assigned, got %d", len(c.nsAssignments))
	}
	for _, pid := range []string{"peer-a", "peer-b", "peer-c"} {
		if _, ok := c.nsAssignments[pid]; !ok {
			t.Errorf("expected %s to be assigned NS flows", pid)
		}
	}
}

func TestBuildNorthSouthAssignments_ModePercentage(t *testing.T) {
	peers := testPeers("peer-a", "peer-b", "peer-c", "peer-d")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "percentage", Percentage: 50}, testNSFlows(1))

	if len(c.nsAssignments) != 2 {
		t.Fatalf("expected 2 peers (50%% of 4), got %d", len(c.nsAssignments))
	}
}

func TestBuildNorthSouthAssignments_PercentageRoundsUp(t *testing.T) {
	peers := testPeers("peer-a", "peer-b", "peer-c")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "percentage", Percentage: 34}, testNSFlows(1))

	if len(c.nsAssignments) != 2 {
		t.Fatalf("expected 2 peers (ceil(34%% of 3)=2), got %d", len(c.nsAssignments))
	}
}

func TestBuildNorthSouthAssignments_PercentageMinOne(t *testing.T) {
	peers := testPeers("peer-a", "peer-b", "peer-c", "peer-d", "peer-e")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "percentage", Percentage: 1}, testNSFlows(1))

	if len(c.nsAssignments) < 1 {
		t.Fatal("expected at least 1 peer even with 1%")
	}
}

func TestBuildNorthSouthAssignments_EmptyFlows(t *testing.T) {
	peers := testPeers("peer-a", "peer-b")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "all"}, nil)

	if len(c.nsAssignments) != 0 {
		t.Fatalf("expected 0 assignments for empty flows, got %d", len(c.nsAssignments))
	}
}

func TestBuildNorthSouthAssignments_NoPeers(t *testing.T) {
	c := newTestCoordinator("peer-a", map[string]*discovery.Peer{})

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "all"}, testNSFlows(1))

	if len(c.nsAssignments) != 0 {
		t.Fatalf("expected 0 assignments with no peers, got %d", len(c.nsAssignments))
	}
}

func TestRotateNSPeers(t *testing.T) {
	peers := testPeers("peer-a", "peer-b", "peer-c", "peer-d")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{
		Mode:           "percentage",
		Percentage:     25,
		RotateInterval: "1m",
	}, testNSFlows(1))

	firstSet := make(map[string]bool)
	for pid := range c.nsAssignments {
		firstSet[pid] = true
	}

	c.mu.Lock()
	c.rotateNSPeers()
	c.mu.Unlock()

	secondSet := make(map[string]bool)
	for pid := range c.nsAssignments {
		secondSet[pid] = true
	}

	if len(firstSet) != 1 || len(secondSet) != 1 {
		t.Fatalf("expected 1 peer per rotation, got first=%d second=%d", len(firstSet), len(secondSet))
	}

	same := true
	for pid := range firstSet {
		if !secondSet[pid] {
			same = false
		}
	}
	if same {
		t.Log("warning: rotation selected same peer (possible with 4 peers at 25%)")
	}
}

func TestRebuildNSOnPeerSetChange(t *testing.T) {
	peers := testPeers("peer-a", "peer-b")
	c := newTestCoordinator("peer-a", peers)

	c.BuildNorthSouthAssignments(config.NorthSouthDistribution{Mode: "all"}, testNSFlows(2))

	if len(c.nsAssignments) != 2 {
		t.Fatalf("expected 2 peers assigned initially, got %d", len(c.nsAssignments))
	}

	newPeers := testPeers("peer-a", "peer-b", "peer-c", "peer-d")
	c.UpdatePeers(newPeers)

	c.mu.Lock()
	if len(c.peers) != c.nsPeerSnapshot {
		c.rebuildNSAssignments()
	}
	c.mu.Unlock()

	if len(c.nsAssignments) != 4 {
		t.Fatalf("expected 4 peers after rebuild, got %d", len(c.nsAssignments))
	}
	for _, pid := range []string{"peer-a", "peer-b", "peer-c", "peer-d"} {
		if _, ok := c.nsAssignments[pid]; !ok {
			t.Errorf("expected %s to be assigned NS flows after rebuild", pid)
		}
	}
}

func TestEffectiveMode(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{"", "one"},
		{"one", "one"},
		{"all", "all"},
		{"percentage", "percentage"},
		{"invalid", "one"},
	}
	for _, tt := range tests {
		d := config.NorthSouthDistribution{Mode: tt.mode}
		if got := d.EffectiveMode(); got != tt.expected {
			t.Errorf("EffectiveMode(%q) = %q, want %q", tt.mode, got, tt.expected)
		}
	}
}
