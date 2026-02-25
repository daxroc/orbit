//go:build linux

package recorder

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/daxroc/orbit/internal/metrics"
)

type SystemRecorder struct {
	nodeName string
	stop     chan struct{}
}

func NewSystemRecorder(nodeName string) *SystemRecorder {
	return &SystemRecorder{
		nodeName: nodeName,
		stop:     make(chan struct{}),
	}
}

func (s *SystemRecorder) Start(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.collect()
			}
		}
	}()
}

func (s *SystemRecorder) Stop() {
	close(s.stop)
}

func (s *SystemRecorder) collect() {
	s.collectSNMP()
	s.collectNetDev()
}

func (s *SystemRecorder) collectSNMP() {
	f, err := os.Open("/proc/net/snmp")
	if err != nil {
		slog.Debug("failed to open /proc/net/snmp", "error", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var headers []string
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		prefix := parts[0]

		if headers == nil || !strings.HasPrefix(headers[0], prefix) {
			headers = strings.Fields(parts[1])
			continue
		}

		values := strings.Fields(parts[1])
		stats := make(map[string]int64, len(headers))
		for i, h := range headers {
			if i < len(values) {
				v, _ := strconv.ParseInt(values[i], 10, 64)
				stats[h] = v
			}
		}
		headers = nil

		switch prefix {
		case "Tcp":
			if v, ok := stats["ActiveOpens"]; ok {
				metrics.NodeTCPActiveOpens.WithLabelValues(s.nodeName).Add(0)
				_ = v
				setCounterToAtLeast(metrics.NodeTCPActiveOpens.WithLabelValues(s.nodeName), float64(v))
			}
			if v, ok := stats["PassiveOpens"]; ok {
				setCounterToAtLeast(metrics.NodeTCPPassiveOpens.WithLabelValues(s.nodeName), float64(v))
			}
		case "Udp":
			if v, ok := stats["OutDatagrams"]; ok {
				setCounterToAtLeast(metrics.NodeUDPDatagramsSent.WithLabelValues(s.nodeName), float64(v))
			}
			if v, ok := stats["InDatagrams"]; ok {
				setCounterToAtLeast(metrics.NodeUDPDatagramsReceived.WithLabelValues(s.nodeName), float64(v))
			}
		}
	}
}

func (s *SystemRecorder) collectNetDev() {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		slog.Debug("failed to open /proc/net/dev", "error", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue
		}
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 10 {
			continue
		}

		rxBytes, _ := strconv.ParseFloat(fields[0], 64)
		txBytes, _ := strconv.ParseFloat(fields[8], 64)

		setCounterToAtLeast(metrics.NodeIPBytesReceived.WithLabelValues(s.nodeName, iface), rxBytes)
		setCounterToAtLeast(metrics.NodeIPBytesSent.WithLabelValues(s.nodeName, iface), txBytes)
	}
}

type counterAdder interface {
	Add(float64)
}

var counterState = make(map[string]float64)

func setCounterToAtLeast(c counterAdder, absolute float64) {
	key := fmt.Sprintf("%p", c)
	prev, ok := counterState[key]
	if !ok || absolute > prev {
		delta := absolute
		if ok {
			delta = absolute - prev
		}
		if delta > 0 {
			c.Add(delta)
		}
		counterState[key] = absolute
	}
}
