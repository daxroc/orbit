package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

type Mode string

const (
	ModeCluster    Mode = "cluster"
	ModeSatellite  Mode = "satellite"
	ModeStandalone Mode = "standalone"
)

type Config struct {
	Mode      Mode   `mapstructure:"mode"`
	PodName   string `mapstructure:"pod-name"`
	Namespace string `mapstructure:"namespace"`
	NodeName  string `mapstructure:"node-name"`
	Zone      string `mapstructure:"zone"`

	GRPCPort int `mapstructure:"grpc-port"`
	HTTPPort int `mapstructure:"http-port"`

	TCPReceiverPortStart int `mapstructure:"tcp-receiver-port-start"`
	UDPReceiverPortStart int `mapstructure:"udp-receiver-port-start"`

	AuthToken string `mapstructure:"auth-token"`

	ServiceName string `mapstructure:"service-name"`

	ProbeInterval   time.Duration `mapstructure:"probe-interval"`
	DiscoveryPeriod time.Duration `mapstructure:"discovery-period"`

	LeaderElectionID        string `mapstructure:"leader-election-id"`
	LeaderElectionNamespace string `mapstructure:"leader-election-namespace"`
	LeaseDuration           time.Duration
	RenewDeadline           time.Duration
	RetryPeriod             time.Duration

	LogLevel  string `mapstructure:"log-level"`
	LogFormat string `mapstructure:"log-format"`

	ScenariosConfigPath string `mapstructure:"scenarios-config-path"`
	ActiveScenario      string `mapstructure:"active-scenario"`

	MetricsProtected bool `mapstructure:"metrics-protected"`

	ScheduleLeaseTTL  time.Duration `mapstructure:"schedule-lease-ttl"`
	HeartbeatInterval time.Duration `mapstructure:"heartbeat-interval"`

	ExternalTargets []ExternalTarget `mapstructure:"external-targets"`
}

type ExternalTarget struct {
	Type            string        `mapstructure:"type"`
	URL             string        `mapstructure:"url"`
	Address         string        `mapstructure:"address"`
	IntervalSeconds int           `mapstructure:"intervalSeconds"`
	Timeout         time.Duration `mapstructure:"timeout"`
}

type SatelliteTarget struct {
	Name      string    `mapstructure:"name" yaml:"name"`
	Host      string    `mapstructure:"host" yaml:"host"`
	HTTPPort  int       `mapstructure:"httpPort" yaml:"httpPort"`
	GRPCPort  int       `mapstructure:"grpcPort" yaml:"grpcPort"`
	TCPPort   int       `mapstructure:"tcpPort" yaml:"tcpPort"`
	UDPPort   int       `mapstructure:"udpPort" yaml:"udpPort"`
	AuthToken string    `mapstructure:"authToken" yaml:"authToken"`
	Flows     []FlowDef `mapstructure:"flows" yaml:"flows"`
}

const (
	DefaultHTTPPort = 8080
	DefaultGRPCPort = 9090
	DefaultTCPPort  = 10000
	DefaultUDPPort  = 11000
)

func (s *SatelliteTarget) ResolveTarget(flowType string) string {
	if s.Host == "" {
		return ""
	}
	switch flowType {
	case "http":
		port := s.HTTPPort
		if port == 0 {
			port = DefaultHTTPPort
		}
		return fmt.Sprintf("%s:%d", s.Host, port)
	case "grpc":
		port := s.GRPCPort
		if port == 0 {
			port = DefaultGRPCPort
		}
		return fmt.Sprintf("%s:%d", s.Host, port)
	case "tcp-stream", "connection-churn":
		port := s.TCPPort
		if port == 0 {
			port = DefaultTCPPort
		}
		return fmt.Sprintf("%s:%d", s.Host, port)
	case "udp-stream":
		port := s.UDPPort
		if port == 0 {
			port = DefaultUDPPort
		}
		return fmt.Sprintf("%s:%d", s.Host, port)
	case "icmp":
		return s.Host
	default:
		return ""
	}
}

type FlowDef struct {
	Type                 string `mapstructure:"type" yaml:"type"`
	URL                  string `mapstructure:"url" yaml:"url"`
	Address              string `mapstructure:"address" yaml:"address"`
	BandwidthMbps        int    `mapstructure:"bandwidthMbps" yaml:"bandwidthMbps"`
	DurationSeconds      int    `mapstructure:"durationSeconds" yaml:"durationSeconds"`
	PayloadBytes         int    `mapstructure:"payloadBytes" yaml:"payloadBytes"`
	RPS                  int    `mapstructure:"rps" yaml:"rps"`
	PacketRate           int    `mapstructure:"packetRate" yaml:"packetRate"`
	PacketSize           int    `mapstructure:"packetSize" yaml:"packetSize"`
	Connections          int    `mapstructure:"connections" yaml:"connections"`
	HTTPMethod           string `mapstructure:"httpMethod" yaml:"httpMethod"`
	KeepAlive            bool   `mapstructure:"keepAlive" yaml:"keepAlive"`
	Pattern              string `mapstructure:"pattern" yaml:"pattern"`
	BurstDurationSeconds int    `mapstructure:"burstDurationSeconds" yaml:"burstDurationSeconds"`
	BurstIntervalSeconds int    `mapstructure:"burstIntervalSeconds" yaml:"burstIntervalSeconds"`
	ConnectionsPerSecond int    `mapstructure:"connectionsPerSecond" yaml:"connectionsPerSecond"`
	HoldDurationMs       int    `mapstructure:"holdDurationMs" yaml:"holdDurationMs"`
	IntervalMs           int    `mapstructure:"intervalMs" yaml:"intervalMs"`
}

type NorthSouthDistribution struct {
	Mode           string `mapstructure:"mode" yaml:"mode"`
	Percentage     int    `mapstructure:"percentage" yaml:"percentage"`
	RotateInterval string `mapstructure:"rotateInterval" yaml:"rotateInterval"`
}

const (
	NSDistModeOne        = "one"
	NSDistModeAll        = "all"
	NSDistModePercentage = "percentage"
)

func (d NorthSouthDistribution) EffectiveMode() string {
	switch d.Mode {
	case NSDistModeAll, NSDistModePercentage:
		return d.Mode
	default:
		return NSDistModeOne
	}
}

type Scenario struct {
	Description            string                 `mapstructure:"description" yaml:"description"`
	EastWest               []FlowDef              `mapstructure:"eastWest" yaml:"eastWest"`
	NorthSouth             []FlowDef              `mapstructure:"northSouth" yaml:"northSouth"`
	NorthSouthDistribution NorthSouthDistribution `mapstructure:"northSouthDistribution" yaml:"northSouthDistribution"`
}

type ScenariosConfig struct {
	ActiveScenario      string              `mapstructure:"activeScenario" yaml:"activeScenario"`
	StabilizationPeriod string              `mapstructure:"stabilizationPeriod" yaml:"stabilizationPeriod"`
	Scenarios           map[string]Scenario `mapstructure:"scenarios" yaml:"scenarios"`
	Satellites          []SatelliteTarget   `mapstructure:"satellites" yaml:"satellites"`
}

func DefaultConfig() *Config {
	return &Config{
		Mode:                    ModeCluster,
		GRPCPort:                DefaultGRPCPort,
		HTTPPort:                DefaultHTTPPort,
		TCPReceiverPortStart:    DefaultTCPPort,
		UDPReceiverPortStart:    DefaultUDPPort,
		ServiceName:             "orbit",
		ProbeInterval:           10 * time.Second,
		DiscoveryPeriod:         5 * time.Second,
		LeaderElectionID:        "orbit-leader",
		LeaderElectionNamespace: "default",
		LeaseDuration:           15 * time.Second,
		RenewDeadline:           10 * time.Second,
		RetryPeriod:             2 * time.Second,
		LogLevel:                "info",
		LogFormat:               "json",
		ScenariosConfigPath:     "/etc/orbit/scenarios.yaml",
		MetricsProtected:        false,
		ScheduleLeaseTTL:        30 * time.Second,
		HeartbeatInterval:       10 * time.Second,
	}
}

func Load() (*Config, error) {
	cfg := DefaultConfig()

	viper.SetConfigName("orbit")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/orbit")
	viper.AddConfigPath(".")
	viper.SetEnvPrefix("ORBIT")
	viper.AutomaticEnv()

	_ = viper.ReadInConfig()

	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if token := os.Getenv("ORBIT_AUTH_TOKEN"); token != "" {
		cfg.AuthToken = token
	}
	if name := os.Getenv("ORBIT_POD_NAME"); name != "" {
		cfg.PodName = name
	}
	if ns := os.Getenv("ORBIT_NAMESPACE"); ns != "" {
		cfg.Namespace = ns
	}
	if node := os.Getenv("ORBIT_NODE_NAME"); node != "" {
		cfg.NodeName = node
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.AuthToken == "" {
		return fmt.Errorf("auth-token is required: set --auth-token, ORBIT_AUTH_TOKEN env, or via config file")
	}
	if c.PodName == "" {
		return fmt.Errorf("pod-name is required: set ORBIT_POD_NAME or --pod-name")
	}
	if c.Mode != ModeCluster && c.Mode != ModeSatellite && c.Mode != ModeStandalone {
		return fmt.Errorf("mode must be 'cluster', 'satellite', or 'standalone', got %q", c.Mode)
	}
	return nil
}
