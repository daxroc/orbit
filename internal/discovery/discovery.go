package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

type Peer struct {
	ID       string
	IP       string
	Address  string
	NodeName string
	Zone     string
	Ready    bool
	LastSeen time.Time
}

type Discovery struct {
	client      kubernetes.Interface
	namespace   string
	serviceName string
	selfID      string
	grpcPort    int
	period      time.Duration

	mu    sync.RWMutex
	peers map[string]*Peer

	onChange func(peers map[string]*Peer)
}

func New(client kubernetes.Interface, namespace, serviceName, selfID string, grpcPort int, period time.Duration) *Discovery {
	return &Discovery{
		client:      client,
		namespace:   namespace,
		serviceName: serviceName,
		selfID:      selfID,
		grpcPort:    grpcPort,
		period:      period,
		peers:       make(map[string]*Peer),
	}
}

func (d *Discovery) SetOnChange(fn func(peers map[string]*Peer)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onChange = fn
}

func (d *Discovery) Peers() map[string]*Peer {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make(map[string]*Peer, len(d.peers))
	for k, v := range d.peers {
		cp := *v
		result[k] = &cp
	}
	return result
}

func (d *Discovery) PeerCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.peers)
}

func (d *Discovery) Run(ctx context.Context) error {
	slog.Info("starting peer discovery", "namespace", d.namespace, "service", d.serviceName)

	if err := d.refresh(ctx); err != nil {
		slog.Warn("initial discovery refresh failed", "error", err)
	}

	go d.watchEndpoints(ctx)

	ticker := time.NewTicker(d.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := d.refresh(ctx); err != nil {
				slog.Warn("discovery refresh failed", "error", err)
			}
		}
	}
}

func (d *Discovery) labelSelector() string {
	return labels.Set{
		discoveryv1.LabelServiceName: d.serviceName,
	}.String()
}

func (d *Discovery) refresh(ctx context.Context) error {
	sliceList, err := d.client.DiscoveryV1().EndpointSlices(d.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: d.labelSelector(),
	})
	if err != nil {
		return fmt.Errorf("list endpointslices: %w", err)
	}

	d.updateFromSlices(sliceList.Items)
	return nil
}

func (d *Discovery) watchEndpoints(ctx context.Context) {
	for {
		watcher, err := d.client.DiscoveryV1().EndpointSlices(d.namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector: d.labelSelector(),
		})
		if err != nil {
			slog.Error("failed to watch endpointslices", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

	loop:
		for {
			select {
			case <-ctx.Done():
				watcher.Stop()
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					break loop
				}
				if event.Type == watch.Error {
					continue
				}
				if _, ok := event.Object.(*discoveryv1.EndpointSlice); ok {
					if err := d.refresh(ctx); err != nil {
						slog.Warn("refresh after watch event failed", "error", err)
					}
				}
			}
		}

		watcher.Stop()

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (d *Discovery) updateFromSlices(slices []discoveryv1.EndpointSlice) {
	d.mu.Lock()
	defer d.mu.Unlock()

	seen := make(map[string]bool)
	now := time.Now()

	for _, slice := range slices {
		for _, ep := range slice.Endpoints {
			peerID := ""
			if ep.TargetRef != nil {
				peerID = ep.TargetRef.Name
			}
			if peerID == "" && len(ep.Addresses) > 0 {
				peerID = ep.Addresses[0]
			}
			if peerID == "" || peerID == d.selfID {
				continue
			}
			if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
				continue
			}
			if len(ep.Addresses) == 0 {
				continue
			}

			seen[peerID] = true
			ip := ep.Addresses[0]
			address := fmt.Sprintf("%s:%d", ip, d.grpcPort)
			nodeName := ""
			if ep.NodeName != nil {
				nodeName = *ep.NodeName
			}
			zone := ""
			if ep.Zone != nil {
				zone = *ep.Zone
			}

			if existing, ok := d.peers[peerID]; ok {
				existing.IP = ip
				existing.Address = address
				existing.NodeName = nodeName
				existing.Zone = zone
				existing.Ready = true
				existing.LastSeen = now
			} else {
				d.peers[peerID] = &Peer{
					ID:       peerID,
					IP:       ip,
					Address:  address,
					NodeName: nodeName,
					Zone:     zone,
					Ready:    true,
					LastSeen: now,
				}
				slog.Info("discovered peer", "id", peerID, "address", address, "node", nodeName, "zone", zone)
			}
		}
	}

	for id := range d.peers {
		if !seen[id] {
			slog.Info("peer removed", "id", id)
			delete(d.peers, id)
		}
	}

	if d.onChange != nil {
		peers := make(map[string]*Peer, len(d.peers))
		for k, v := range d.peers {
			cp := *v
			peers[k] = &cp
		}
		go d.onChange(peers)
	}
}
