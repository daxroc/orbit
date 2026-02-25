package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

type Peer struct {
	ID       string
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

func (d *Discovery) refresh(ctx context.Context) error {
	endpoints, err := d.client.CoreV1().Endpoints(d.namespace).Get(ctx, d.serviceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get endpoints: %w", err)
	}

	d.updateFromEndpoints(endpoints)
	return nil
}

func (d *Discovery) watchEndpoints(ctx context.Context) {
	for {
		watcher, err := d.client.CoreV1().Endpoints(d.namespace).Watch(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", d.serviceName),
		})
		if err != nil {
			slog.Error("failed to watch endpoints", "error", err)
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
				if ep, ok := event.Object.(*corev1.Endpoints); ok {
					d.updateFromEndpoints(ep)
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

func (d *Discovery) updateFromEndpoints(endpoints *corev1.Endpoints) {
	d.mu.Lock()
	defer d.mu.Unlock()

	seen := make(map[string]bool)
	now := time.Now()

	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.Addresses {
			peerID := ""
			if addr.TargetRef != nil {
				peerID = addr.TargetRef.Name
			}
			if peerID == "" {
				peerID = addr.IP
			}
			if peerID == d.selfID {
				continue
			}

			seen[peerID] = true
			address := fmt.Sprintf("%s:%d", addr.IP, d.grpcPort)
			nodeName := ""
			if addr.NodeName != nil {
				nodeName = *addr.NodeName
			}
			zone := ""

			if existing, ok := d.peers[peerID]; ok {
				existing.Address = address
				existing.NodeName = nodeName
				existing.Ready = true
				existing.LastSeen = now
			} else {
				d.peers[peerID] = &Peer{
					ID:       peerID,
					Address:  address,
					NodeName: nodeName,
					Zone:     zone,
					Ready:    true,
					LastSeen: now,
				}
				slog.Info("discovered peer", "id", peerID, "address", address, "node", nodeName)
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
