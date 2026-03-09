package metrics

// NOTE: run_id is used as a Prometheus label on high-cardinality metrics.
// Each scenario activation creates a new timestamp-based run_id, permanently
// adding new timeseries. For long-running deployments with frequent scenario
// activations, tune Prometheus TSDB retention or --query.max-samples limits
// to avoid OOM. See README.md "Operational Notes" for guidance.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	defaultBuckets = []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

	AppBytesSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "bytes_sent_total",
		Help:      "Total bytes written to sockets at the application layer.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target", "direction"})

	AppBytesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "bytes_received_total",
		Help:      "Total bytes read from sockets at the application layer.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target", "direction"})

	AppPacketsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "packets_sent_total",
		Help:      "Total UDP/ICMP packets sent.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target"})

	AppPacketsReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "packets_received_total",
		Help:      "Total UDP/ICMP packets received.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target"})

	AppConnectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "connections_total",
		Help:      "Total TCP/gRPC connections established.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target"})

	AppConnectionsActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "connections_active",
		Help:      "Currently open connections.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target"})

	AppRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "request_duration_seconds",
		Help:      "HTTP/gRPC request round-trip latency.",
		Buckets:   defaultBuckets,
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target"})

	AppThroughput = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "throughput_bytes_per_second",
		Help:      "Current measured throughput.",
	}, []string{"scenario", "run_id", "flow_type", "protocol", "source", "target"})

	WireBytesSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "bytes_sent_total",
		Help:      "Bytes sent from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireBytesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "bytes_received_total",
		Help:      "Bytes received from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireBytesRetransmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "bytes_retransmitted_total",
		Help:      "Retransmitted bytes from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireSegmentsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "segments_sent_total",
		Help:      "TCP segments sent.",
	}, []string{"source", "target", "protocol"})

	WireSegmentsRetransmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "segments_retransmitted_total",
		Help:      "TCP segments retransmitted.",
	}, []string{"source", "target", "protocol"})

	WireRTT = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "rtt_seconds",
		Help:      "Smoothed RTT from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireRTTVariance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "rtt_variance_seconds",
		Help:      "RTT variance from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireMSS = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "mss_bytes",
		Help:      "Max segment size from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireCwnd = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "cwnd_segments",
		Help:      "Congestion window from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	WireLostPackets = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "wire",
		Name:      "lost_packets_total",
		Help:      "Lost packets from TCP_INFO.",
	}, []string{"source", "target", "protocol"})

	NodeTCPActiveOpens = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "node",
		Name:      "tcp_active_opens_total",
		Help:      "TCP active opens from /proc/net/snmp.",
	}, []string{"node"})

	NodeTCPPassiveOpens = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "node",
		Name:      "tcp_passive_opens_total",
		Help:      "TCP passive opens from /proc/net/snmp.",
	}, []string{"node"})

	NodeIPBytesSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "node",
		Name:      "ip_bytes_sent_total",
		Help:      "Interface-level bytes out from /proc/net/dev.",
	}, []string{"node", "interface"})

	NodeIPBytesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "node",
		Name:      "ip_bytes_received_total",
		Help:      "Interface-level bytes in from /proc/net/dev.",
	}, []string{"node", "interface"})

	NodeUDPDatagramsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "node",
		Name:      "udp_datagrams_sent_total",
		Help:      "UDP datagrams sent from /proc/net/snmp.",
	}, []string{"node"})

	NodeUDPDatagramsReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "node",
		Name:      "udp_datagrams_received_total",
		Help:      "UDP datagrams received from /proc/net/snmp.",
	}, []string{"node"})

	PeerCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "orbit",
		Name:      "peer_count",
		Help:      "Number of discovered peers.",
	})

	LeaderInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Name:      "leader_info",
		Help:      "Whether this instance is the leader (1) or not (0).",
	}, []string{"instance"})

	ScenarioInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orbit",
		Name:      "scenario_active",
		Help:      "Currently active scenario (1 if active).",
	}, []string{"scenario", "run_id"})

	DNSResolutionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "dns_resolution_seconds",
		Help:      "DNS lookup latency.",
		Buckets:   defaultBuckets,
	}, []string{"target", "source"})

	ChecksumErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "app",
		Name:      "checksum_errors_total",
		Help:      "Payload checksum verification failures.",
	}, []string{"flow_type", "protocol", "source", "target"})

	GeneratorBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "generator",
		Name:      "bytes_total",
		Help:      "Bytes generated per type/target.",
	}, []string{"flow_type", "source", "target"})

	GeneratorErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "generator",
		Name:      "errors_total",
		Help:      "Generator errors per type/target.",
	}, []string{"flow_type", "source", "target", "reason"})

	GeneratorLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "orbit",
		Subsystem: "generator",
		Name:      "latency_seconds",
		Help:      "Request latency histogram per type/target.",
		Buckets:   defaultBuckets,
	}, []string{"flow_type", "source", "target"})

	ReceiverBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "receiver",
		Name:      "bytes_total",
		Help:      "Bytes received per type.",
	}, []string{"receiver_type"})

	ReceiverConnections = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "receiver",
		Name:      "connections_total",
		Help:      "Connections accepted per type.",
	}, []string{"receiver_type"})

	ReceiverRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orbit",
		Subsystem: "receiver",
		Name:      "requests_total",
		Help:      "Requests handled per receiver type.",
	}, []string{"receiver_type"})
)
