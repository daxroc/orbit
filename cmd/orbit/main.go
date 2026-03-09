package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daxroc/orbit/internal/agent"
	"github.com/daxroc/orbit/internal/config"
	"github.com/daxroc/orbit/internal/debug"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Name:      "build_info",
		Help:      "Build information for the orbit binary.",
	}, []string{"version", "commit"}).WithLabelValues(version, commit).Set(1)

	root := &cobra.Command{
		Use:     "orbit",
		Short:   "Network load generator and measurement tool for eBPF validation",
		Version: version,
		RunE:    run,
	}

	flags := root.Flags()
	flags.String("mode", "cluster", "Operating mode: cluster or satellite")
	flags.String("pod-name", "", "Pod name (usually from downward API)")
	flags.String("namespace", "", "Kubernetes namespace (defaults to ORBIT_NAMESPACE)")
	flags.String("node-name", "", "Node name (from downward API)")
	flags.String("zone", "", "Topology zone")
	flags.Int("grpc-port", 9090, "gRPC server port")
	flags.Int("http-port", 8080, "HTTP server port")
	flags.Int("tcp-receiver-port-start", 10000, "TCP receiver starting port")
	flags.Int("udp-receiver-port-start", 11000, "UDP receiver starting port")
	flags.String("auth-token", "", "Shared authentication token")
	flags.String("service-name", "orbit", "Headless service name for discovery")
	flags.Duration("probe-interval", 10*time.Second, "Default probe interval")
	flags.Duration("discovery-period", 5*time.Second, "Peer discovery refresh period")
	flags.String("leader-election-id", "orbit-leader", "Leader election lease name")
	flags.String("leader-election-namespace", "", "Leader election namespace (defaults to ORBIT_NAMESPACE)")
	flags.String("log-level", "info", "Log level (debug, info, warn, error)")
	flags.String("log-format", "json", "Log format (json, text)")
	flags.StringSlice("debug-components", nil,
		"Verbose debug for specific components: tcp-generator,tcp-receiver,wire,churn,coordinator,discovery,all")
	flags.String("scenarios-config-path", "/etc/orbit/scenarios.yaml", "Path to scenarios config file")
	flags.String("active-scenario", "", "Scenario to activate on leader election")
	flags.Bool("metrics-protected", false, "Require auth token for /metrics endpoint")
	flags.Bool("tls-enabled", false, "enable TLS for leader→peer gRPC connections")
	flags.String("tls-ca-file", "", "path to CA certificate file for TLS verification (empty = system pool)")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if v, _ := cmd.Flags().GetString("mode"); v != "" {
		cfg.Mode = config.Mode(v)
	}
	if v, _ := cmd.Flags().GetString("pod-name"); v != "" {
		cfg.PodName = v
	}
	if v, _ := cmd.Flags().GetString("namespace"); v != "" {
		cfg.Namespace = v
	}
	if v, _ := cmd.Flags().GetString("node-name"); v != "" {
		cfg.NodeName = v
	}
	if v, _ := cmd.Flags().GetString("auth-token"); v != "" {
		cfg.AuthToken = v
	}
	if v, _ := cmd.Flags().GetString("service-name"); v != "" {
		cfg.ServiceName = v
	}
	if v, _ := cmd.Flags().GetString("scenarios-config-path"); v != "" {
		cfg.ScenariosConfigPath = v
	}
	if v, _ := cmd.Flags().GetString("active-scenario"); v != "" {
		cfg.ActiveScenario = v
	}
	if v, _ := cmd.Flags().GetInt("grpc-port"); v != 0 {
		cfg.GRPCPort = v
	}
	if v, _ := cmd.Flags().GetInt("http-port"); v != 0 {
		cfg.HTTPPort = v
	}
	if v, _ := cmd.Flags().GetBool("metrics-protected"); v {
		cfg.MetricsProtected = v
	}
	if v, _ := cmd.Flags().GetBool("tls-enabled"); v {
		cfg.TLSEnabled = v
	}
	if v, _ := cmd.Flags().GetString("tls-ca-file"); v != "" {
		cfg.TLSCAFile = v
	}

	setupLogging(cfg.LogLevel, cfg.LogFormat)

	if comps, _ := cmd.Flags().GetStringSlice("debug-components"); len(comps) > 0 {
		debug.Set(comps)
	}

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		return err
	}

	a, err := agent.New(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	go func() {
		if err := a.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("agent failed", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	a.Stop(shutdownCtx)

	slog.Info("orbit shutdown complete")
	return nil
}

func setupLogging(level, format string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}
