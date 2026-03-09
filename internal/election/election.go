package election

import (
	"context"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

type Callbacks struct {
	OnStartedLeading func(ctx context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(identity string)
}

type Election struct {
	client    kubernetes.Interface
	lockName  string
	namespace string
	identity  string
	cfg       Config
	callbacks Callbacks
}

type Config struct {
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

func New(client kubernetes.Interface, lockName, namespace, identity string, cfg Config, cb Callbacks) *Election {
	return &Election{
		client:    client,
		lockName:  lockName,
		namespace: namespace,
		identity:  identity,
		cfg:       cfg,
		callbacks: cb,
	}
}

func (e *Election) Run(ctx context.Context) {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      e.lockName,
			Namespace: e.namespace,
		},
		Client: e.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: e.identity,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.cfg.LeaseDuration,
		RenewDeadline:   e.cfg.RenewDeadline,
		RetryPeriod:     e.cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				slog.Info("started leading", "identity", e.identity)
				if e.callbacks.OnStartedLeading != nil {
					e.callbacks.OnStartedLeading(ctx)
				}
			},
			OnStoppedLeading: func() {
				slog.Info("stopped leading", "identity", e.identity)
				if e.callbacks.OnStoppedLeading != nil {
					e.callbacks.OnStoppedLeading()
				}
				// Intentional Kubernetes leader-election pattern: exit immediately so
				// Kubernetes restarts the pod and triggers a new election. Performing a
				// full graceful shutdown here would delay pod restart while the new leader
				// may have already redistributed schedules, causing a gap in coverage.
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				if identity == e.identity {
					return
				}
				slog.Info("new leader elected", "leader", identity)
				if e.callbacks.OnNewLeader != nil {
					e.callbacks.OnNewLeader(identity)
				}
			},
		},
	})
}
