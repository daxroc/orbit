package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/config"
	"github.com/daxroc/orbit/internal/coordinator"
	"github.com/daxroc/orbit/internal/discovery"
	"github.com/daxroc/orbit/internal/election"
	"github.com/daxroc/orbit/internal/generator"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/receiver"
	"github.com/daxroc/orbit/internal/recorder"
	"github.com/daxroc/orbit/internal/scenario"
	"github.com/daxroc/orbit/internal/server"
	orbitv1 "github.com/daxroc/orbit/proto/orbit/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Agent struct {
	cfg         *config.Config
	validator   *auth.TokenValidator
	httpServer  *server.HTTPServer
	grpcServer  *server.GRPCServer
	disc        *discovery.Discovery
	coord       *coordinator.Coordinator
	genMgr      *generator.Manager
	recvMgr     *receiver.Manager
	appRec      *recorder.AppRecorder
	wireRec     *recorder.WireRecorder
	scenEngine  *scenario.Engine
	scenWatcher *scenario.Watcher
	kubeClient  kubernetes.Interface
	startTime   time.Time

	mu       sync.RWMutex
	ctx      context.Context
	isLeader bool
	leaderID string
}

func New(cfg *config.Config) (*Agent, error) {
	validator := auth.NewTokenValidator(cfg.AuthToken)
	appRec := recorder.NewAppRecorder()
	wireRec := recorder.NewWireRecorder(cfg.PodName)

	a := &Agent{
		cfg:        cfg,
		validator:  validator,
		appRec:     appRec,
		wireRec:    wireRec,
		scenEngine: scenario.NewEngine(),
		startTime:  time.Now(),
	}

	a.httpServer = server.NewHTTPServer(cfg.HTTPPort, validator, cfg.MetricsProtected)
	a.httpServer.SetStatusFunc(a.statusFunc)

	a.grpcServer = server.NewGRPCServer(cfg.GRPCPort, cfg.PodName, validator)
	a.grpcServer.SetOnSchedule(a.handleScheduleAssignment)

	a.genMgr = generator.NewManager(appRec)
	a.recvMgr = receiver.NewManager(validator, appRec)

	a.coord = coordinator.New(cfg.PodName, validator, coordinator.PortConfig{
		GRPC:             cfg.GRPCPort,
		HTTPEcho:         cfg.HTTPPort + 1,
		TCPReceiverStart: cfg.TCPReceiverPortStart,
		UDPReceiverStart: cfg.UDPReceiverPortStart,
	})

	return a, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	slog.Info("starting orbit agent",
		"mode", a.cfg.Mode,
		"pod", a.cfg.PodName,
		"node", a.cfg.NodeName,
	)

	a.scenEngine.SetOnActiveChange(a.onActiveScenarioChange)

	if err := a.scenEngine.LoadFromFile(a.cfg.ScenariosConfigPath); err != nil {
		slog.Warn("failed to load scenarios config", "path", a.cfg.ScenariosConfigPath, "error", err)
	}

	a.scenWatcher = scenario.NewWatcher(a.scenEngine, a.cfg.ScenariosConfigPath)
	if err := a.scenWatcher.Start(); err != nil {
		slog.Warn("failed to start scenario config watcher", "error", err)
	}

	a.setupReceivers()

	go func() {
		if err := a.httpServer.Start(); err != nil {
			slog.Error("HTTP server failed", "error", err)
		}
	}()
	go func() {
		if err := a.grpcServer.Start(); err != nil {
			slog.Error("gRPC server failed", "error", err)
		}
	}()

	if err := a.recvMgr.StartAll(ctx); err != nil {
		return fmt.Errorf("start receivers: %w", err)
	}

	switch a.cfg.Mode {
	case config.ModeCluster:
		if err := a.runClusterMode(ctx); err != nil {
			return err
		}
	case config.ModeStandalone:
		slog.Info("running in standalone mode — local generators and receivers only")
		a.runStandaloneMode(ctx)
	default:
		slog.Info("running in satellite mode — receivers only")
		<-ctx.Done()
	}

	return nil
}

func (a *Agent) Stop(ctx context.Context) {
	slog.Info("shutting down orbit agent")

	if a.scenWatcher != nil {
		a.scenWatcher.Stop()
	}
	a.genMgr.StopAll()
	a.recvMgr.StopAll()
	a.grpcServer.Stop()

	if err := a.httpServer.Stop(ctx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}
}

func (a *Agent) runClusterMode(ctx context.Context) error {
	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("k8s in-cluster config: %w", err)
	}
	a.kubeClient, err = kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("k8s client: %w", err)
	}

	ns := a.cfg.Namespace
	if ns == "" {
		ns = a.cfg.LeaderElectionNamespace
	}

	a.disc = discovery.New(
		a.kubeClient,
		ns,
		a.cfg.ServiceName,
		a.cfg.PodName,
		a.cfg.GRPCPort,
		a.cfg.DiscoveryPeriod,
	)

	a.disc.SetOnChange(func(peers map[string]*discovery.Peer) {
		metrics.PeerCount.Set(float64(len(peers)))
		a.coord.UpdatePeers(peers)
	})

	go func() {
		if err := a.disc.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("discovery failed", "error", err)
		}
	}()

	el := election.New(
		a.kubeClient,
		a.cfg.LeaderElectionID,
		ns,
		a.cfg.PodName,
		election.Config{
			LeaseDuration: a.cfg.LeaseDuration,
			RenewDeadline: a.cfg.RenewDeadline,
			RetryPeriod:   a.cfg.RetryPeriod,
		},
		election.Callbacks{
			OnStartedLeading: a.onStartedLeading,
			OnStoppedLeading: a.onStoppedLeading,
			OnNewLeader:      a.onNewLeader,
		},
	)

	go el.Run(ctx)

	<-ctx.Done()
	return nil
}

func (a *Agent) onStartedLeading(ctx context.Context) {
	a.mu.Lock()
	a.isLeader = true
	a.leaderID = a.cfg.PodName
	a.mu.Unlock()

	metrics.LeaderInfo.WithLabelValues(a.cfg.PodName).Set(1)
	a.coord.SetActive(true)

	slog.Info("elected as leader, will orchestrate scenarios")

	scenarioName, _ := a.scenEngine.Active()
	if scenarioName == "" {
		scenarioName = a.cfg.ActiveScenario
	}
	if scenarioName == "" {
		slog.Info("no active scenario configured")
		return
	}

	a.activateScenario(ctx, scenarioName)
}

func (a *Agent) onActiveScenarioChange(scenarioName string) {
	a.mu.RLock()
	isLeader := a.isLeader
	ctx := a.ctx
	a.mu.RUnlock()

	if !isLeader {
		slog.Info("active scenario changed but not leader, ignoring", "scenario", scenarioName)
		return
	}

	if scenarioName == "" {
		slog.Info("active scenario cleared, stopping flows")
		a.genMgr.StopAll()
		a.scenEngine.Clear()
		return
	}

	a.activateScenario(ctx, scenarioName)
}

func (a *Agent) activateScenario(ctx context.Context, scenarioName string) {
	sc, ok := a.scenEngine.Get(scenarioName)
	if !ok {
		slog.Error("configured scenario not found", "scenario", scenarioName)
		return
	}

	slog.Info("activating scenario", "scenario", scenarioName)
	a.genMgr.StopAll()

	if a.disc != nil {
		if !a.waitForStablePeers(ctx) {
			return
		}
	}

	templates := make([]coordinator.FlowTemplate, 0, len(sc.EastWest))
	for _, f := range sc.EastWest {
		templates = append(templates, coordinator.FlowTemplate{
			Type:                 f.Type,
			BandwidthBps:         int64(f.BandwidthMbps) * 1_000_000,
			PacketRate:           int32(f.PacketRate),
			PacketSize:           int32(f.PacketSize),
			RPS:                  int32(f.RPS),
			PayloadBytes:         int32(f.PayloadBytes),
			Connections:          int32(f.Connections),
			HTTPMethod:           f.HTTPMethod,
			KeepAlive:            f.KeepAlive,
			Pattern:              f.Pattern,
			BurstDurationSeconds: int32(f.BurstDurationSeconds),
			BurstIntervalSeconds: int32(f.BurstIntervalSeconds),
			ConnectionsPerSecond: int32(f.ConnectionsPerSecond),
			HoldDurationMs:       int32(f.HoldDurationMs),
			DurationSeconds:      int32(f.DurationSeconds),
			IntervalMs:           int32(f.IntervalMs),
		})
	}

	assignments := a.coord.BuildMeshAssignments(scenarioName, templates)
	if err := a.coord.DistributeSchedule(ctx, scenarioName, assignments); err != nil {
		slog.Error("failed to distribute schedule", "error", err)
	}

	a.startNorthSouthFlows(ctx, scenarioName, sc)
}

func (a *Agent) startNorthSouthFlows(ctx context.Context, scenarioName string, sc config.Scenario) {
	if len(sc.NorthSouth) == 0 {
		return
	}

	runID := fmt.Sprintf("%s-ns-%d", scenarioName, time.Now().UnixMilli())
	slog.Info("starting north-south flows", "scenario", scenarioName, "flows", len(sc.NorthSouth))

	for i, f := range sc.NorthSouth {
		target := f.URL
		if target == "" {
			target = f.Address
		}
		if target == "" {
			slog.Warn("north-south flow missing target URL/address, skipping", "index", i)
			continue
		}

		labels := generator.Labels{
			Scenario: scenarioName,
			RunID:    runID,
			FlowType: f.Type,
			Protocol: f.Type,
			Source:   a.cfg.PodName,
			Target:   target,
		}

		var gen generator.Generator
		dur := time.Duration(f.DurationSeconds) * time.Second

		switch f.Type {
		case "http":
			gen = generator.NewHTTPGenerator(
				fmt.Sprintf("ns-%d", i), labels, target,
				f.RPS, f.PayloadBytes, f.HTTPMethod, f.KeepAlive,
				dur, a.validator, a.appRec,
			)
		case "tcp-stream":
			gen = generator.NewTCPGenerator(
				fmt.Sprintf("ns-%d", i), labels, target,
				int64(f.BandwidthMbps)*1_000_000, f.PayloadBytes, f.Connections,
				dur, f.Pattern, a.validator, a.appRec,
			)
		case "udp-stream":
			gen = generator.NewUDPGenerator(
				fmt.Sprintf("ns-%d", i), labels, target,
				f.PacketRate, f.PacketSize,
				dur, a.validator, a.appRec,
			)
		case "icmp":
			intervalMs := 1000
			if f.IntervalMs > 0 {
				intervalMs = f.IntervalMs
			}
			gen = generator.NewICMPGenerator(
				fmt.Sprintf("ns-%d", i), labels, target,
				intervalMs, dur, a.appRec,
			)
		default:
			slog.Warn("unsupported north-south flow type", "type", f.Type)
			continue
		}

		a.genMgr.Add(gen)
	}

	if err := a.genMgr.StartAll(ctx); err != nil {
		slog.Error("failed to start north-south generators", "error", err)
	}
}

func (a *Agent) waitForStablePeers(ctx context.Context) bool {
	stabPeriod := a.scenEngine.StabilizationPeriod()
	maxWait := 3 * stabPeriod
	pollInterval := 2 * time.Second

	slog.Info("waiting for peer mesh to stabilize",
		"stabilizationPeriod", stabPeriod,
		"maxWait", maxWait,
	)

	deadline := time.After(maxWait)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	lastCount := a.disc.PeerCount()
	stableSince := time.Now()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			current := a.disc.PeerCount()
			if current == 0 {
				slog.Warn("no peers discovered after max wait, cannot distribute schedule")
				return false
			}
			slog.Info("max stabilization wait reached, proceeding", "peers", current)
			return true
		case <-ticker.C:
			current := a.disc.PeerCount()
			if current != lastCount {
				slog.Info("peer count changed, resetting stability timer",
					"previous", lastCount,
					"current", current,
				)
				lastCount = current
				stableSince = time.Now()
			} else if current > 0 && time.Since(stableSince) >= stabPeriod {
				slog.Info("peer mesh stable", "peers", current, "stableFor", time.Since(stableSince).Round(time.Second))
				return true
			}
		}
	}
}

func (a *Agent) onStoppedLeading() {
	a.mu.Lock()
	a.isLeader = false
	a.mu.Unlock()

	metrics.LeaderInfo.WithLabelValues(a.cfg.PodName).Set(0)
	a.coord.SetActive(false)
	a.genMgr.StopAll()
}

func (a *Agent) onNewLeader(identity string) {
	a.mu.Lock()
	a.leaderID = identity
	a.mu.Unlock()
}

func (a *Agent) handleScheduleAssignment(_ context.Context, sched *orbitv1.ProbeSchedule) (*orbitv1.ProbeScheduleResponse, error) {
	slog.Info("received schedule assignment",
		"scenario", sched.ScenarioName,
		"run_id", sched.RunId,
		"east_west_flows", len(sched.EastWestFlows),
		"north_south_flows", len(sched.NorthSouthFlows),
	)

	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()

	a.genMgr.StopAll()
	a.scenEngine.SetActive(sched.ScenarioName, sched.RunId)

	for _, flow := range sched.EastWestFlows {
		labels := generator.Labels{
			Scenario: sched.ScenarioName,
			RunID:    sched.RunId,
			FlowType: flow.Type,
			Protocol: flow.Type,
			Source:   a.cfg.PodName,
			Target:   flow.TargetAddress,
		}

		var gen generator.Generator
		switch flow.Type {
		case "tcp-stream":
			dur := time.Duration(0)
			if flow.Duration != nil {
				dur = flow.Duration.AsDuration()
			}
			gen = generator.NewTCPGenerator(
				flow.Id, labels, flow.TargetAddress,
				flow.BandwidthBps, int(flow.PayloadBytes), int(flow.Connections),
				dur, flow.Pattern, a.validator, a.appRec,
			)
		case "udp-stream":
			dur := time.Duration(0)
			if flow.Duration != nil {
				dur = flow.Duration.AsDuration()
			}
			gen = generator.NewUDPGenerator(
				flow.Id, labels, flow.TargetAddress,
				int(flow.PacketRate), int(flow.PacketSize),
				dur, a.validator, a.appRec,
			)
		case "http":
			dur := time.Duration(0)
			if flow.Duration != nil {
				dur = flow.Duration.AsDuration()
			}
			gen = generator.NewHTTPGenerator(
				flow.Id, labels, flow.TargetAddress,
				int(flow.Rps), int(flow.PayloadBytes),
				flow.HttpMethod, flow.KeepAlive,
				dur, a.validator, a.appRec,
			)
		case "grpc":
			dur := time.Duration(0)
			if flow.Duration != nil {
				dur = flow.Duration.AsDuration()
			}
			gen = generator.NewGRPCGenerator(
				flow.Id, labels, flow.TargetAddress,
				int(flow.Rps), int(flow.PayloadBytes),
				dur, a.validator, a.appRec,
			)
		case "icmp":
			dur := time.Duration(0)
			if flow.Duration != nil {
				dur = flow.Duration.AsDuration()
			}
			intervalMs := 1000
			if flow.Interval != nil {
				intervalMs = int(flow.Interval.AsDuration().Milliseconds())
			}
			gen = generator.NewICMPGenerator(
				flow.Id, labels, flow.TargetAddress,
				intervalMs, dur, a.appRec,
			)
		case "connection-churn":
			dur := time.Duration(0)
			if flow.Duration != nil {
				dur = flow.Duration.AsDuration()
			}
			gen = generator.NewChurnGenerator(
				flow.Id, labels, flow.TargetAddress,
				int(flow.ConnectionsPerSecond), int(flow.HoldDurationMs),
				dur, a.validator, a.appRec,
			)
		default:
			slog.Warn("unknown flow type", "type", flow.Type)
			continue
		}

		a.genMgr.Add(gen)
	}

	if err := a.genMgr.StartAll(ctx); err != nil {
		return &orbitv1.ProbeScheduleResponse{Accepted: false, Error: err.Error()}, nil
	}

	return &orbitv1.ProbeScheduleResponse{Accepted: true}, nil
}

func (a *Agent) setupReceivers() {
	a.recvMgr.Add(receiver.NewTCPReceiver(a.cfg.TCPReceiverPortStart, a.validator, a.appRec))
	a.recvMgr.Add(receiver.NewUDPReceiver(a.cfg.UDPReceiverPortStart, a.validator, a.appRec))
	a.recvMgr.Add(receiver.NewHTTPReceiver(a.cfg.HTTPPort+1, a.validator, a.appRec))
}

func (a *Agent) runStandaloneMode(ctx context.Context) {
	scenarioName := a.cfg.ActiveScenario
	if scenarioName != "" {
		sc, ok := a.scenEngine.Get(scenarioName)
		if ok {
			slog.Info("starting standalone scenario", "scenario", scenarioName)
			a.scenEngine.SetActive(scenarioName, fmt.Sprintf("%s-%d", scenarioName, time.Now().UnixMilli()))

			for i, f := range sc.EastWest {
				labels := generator.Labels{
					Scenario: scenarioName,
					RunID:    scenarioName,
					FlowType: f.Type,
					Protocol: f.Type,
					Source:   a.cfg.PodName,
					Target:   fmt.Sprintf("localhost:%d", a.cfg.TCPReceiverPortStart+i),
				}

				var gen generator.Generator
				switch f.Type {
				case "tcp-stream":
					gen = generator.NewTCPGenerator(
						fmt.Sprintf("standalone-%d", i), labels,
						fmt.Sprintf("localhost:%d", a.cfg.TCPReceiverPortStart),
						int64(f.BandwidthMbps)*1_000_000, f.PayloadBytes, f.Connections,
						time.Duration(f.DurationSeconds)*time.Second, f.Pattern, a.validator, a.appRec,
					)
				case "udp-stream":
					gen = generator.NewUDPGenerator(
						fmt.Sprintf("standalone-%d", i), labels,
						fmt.Sprintf("localhost:%d", a.cfg.UDPReceiverPortStart),
						f.PacketRate, f.PacketSize,
						time.Duration(f.DurationSeconds)*time.Second, a.validator, a.appRec,
					)
				default:
					slog.Warn("standalone mode: unsupported flow type, skipping", "type", f.Type)
					continue
				}
				a.genMgr.Add(gen)
			}

			if err := a.genMgr.StartAll(ctx); err != nil {
				slog.Error("failed to start standalone generators", "error", err)
			}
		} else {
			slog.Error("configured scenario not found", "scenario", scenarioName)
		}
	}

	<-ctx.Done()
}

func (a *Agent) statusFunc() server.StatusResponse {
	a.mu.RLock()
	defer a.mu.RUnlock()

	scenName, runID := a.scenEngine.Active()

	peerCount := 0
	if a.disc != nil {
		peerCount = a.disc.PeerCount()
	}

	return server.StatusResponse{
		PodName:        a.cfg.PodName,
		Mode:           string(a.cfg.Mode),
		Leader:         a.isLeader,
		LeaderID:       a.leaderID,
		PeerCount:      peerCount,
		ActiveScenario: scenName,
		RunID:          runID,
		ActiveFlows:    a.genMgr.Count(),
		Uptime:         time.Since(a.startTime).String(),
	}
}
