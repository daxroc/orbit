# Orbit Satellite — Docker Compose

Run an Orbit satellite instance outside the Kubernetes cluster using Docker Compose. The satellite acts as a controlled external endpoint for north-south traffic measurement.

## Prerequisites

- Docker with Compose v2+
- The `ORBIT_AUTH_TOKEN` must match the token used by the in-cluster Orbit agents

## Quick Start

```bash
export ORBIT_AUTH_TOKEN=my-secret-token
docker compose -f satellite-compose.yaml up -d
```

## Configuration

All configuration is done via environment variables in `satellite-compose.yaml`.

| Variable | Default | Description |
|----------|---------|-------------|
| `ORBIT_AUTH_TOKEN` | — | **Required.** Shared bearer token (must match cluster agents) |
| `ORBIT_POD_NAME` | `satellite-01` | Instance identifier shown in metrics and peer lists |
| `ORBIT_LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `ORBIT_LOG_FORMAT` | `text` | Log format (`json` or `text`) |
| `ORBIT_HTTP_PORT` | `8080` | HTTP server port (health, metrics, status) |
| `ORBIT_GRPC_PORT` | `9090` | gRPC server port (schedule coordination) |

## Exposed Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| 8080 | HTTP | Health probes, Prometheus metrics, status API |
| 9090 | gRPC | Schedule coordination with cluster leader |
| 10000 | TCP | TCP stream receiver |
| 11000 | UDP | UDP stream receiver |

## Verifying the Satellite

```bash
# Health check
curl http://localhost:8080/healthz

# Prometheus metrics
curl http://localhost:8080/metrics

# Agent status (requires auth)
curl -H "Authorization: Bearer ${ORBIT_AUTH_TOKEN}" http://localhost:8080/status
```

## Connecting from the Cluster

Register the satellite under `northSouth.satellites` in your Helm values. You only need to specify the host once — target URLs and addresses are resolved automatically from the satellite's standard ports:

```yaml
northSouth:
  satellites:
    - name: satellite-01
      host: "<SATELLITE_HOST>"
      # ports default to 8080/9090/10000/11000 — override if needed
      # authToken: ""    # defaults to the cluster's auth.token
      flows:
        - type: http
          rps: 50
          payloadBytes: 1024
        - type: tcp-stream
          bandwidthMbps: 10
          payloadBytes: 1400
        - type: udp-stream
          packetRate: 1000
          packetSize: 1400
```

Replace `<SATELLITE_HOST>` with the IP or hostname reachable from the cluster. Then activate any scenario:

```bash
helm upgrade orbit deploy/helm/orbit --reuse-values \
  --set config.activeScenario="steady-low"
```

Satellite flows are started automatically with every scenario activation. They appear in Prometheus metrics with `direction="north-south"` and generator IDs prefixed with `sat-<name>-`.

You can also define additional north-south flows directly in a scenario's `northSouth` field (with explicit URLs) — both sources are merged at activation time.

## Running Multiple Satellites

Duplicate the service block with unique names and ports:

```yaml
services:
  satellite-01:
    image: dcroche/orbit:latest
    command: ["--mode=satellite"]
    environment:
      ORBIT_AUTH_TOKEN: "${ORBIT_AUTH_TOKEN:?Set ORBIT_AUTH_TOKEN}"
      ORBIT_POD_NAME: "satellite-01"
    ports:
      - "8080:8080"
      - "9090:9090"
      - "10000:10000"
      - "11000:11000"

  satellite-02:
    image: dcroche/orbit:latest
    command: ["--mode=satellite"]
    environment:
      ORBIT_AUTH_TOKEN: "${ORBIT_AUTH_TOKEN:?Set ORBIT_AUTH_TOKEN}"
      ORBIT_POD_NAME: "satellite-02"
    ports:
      - "8180:8080"
      - "9190:9090"
      - "10100:10000"
      - "11100:11000"
```

## Collecting Satellite Metrics

The satellite exposes Prometheus metrics at `:8080/metrics` (bytes received, active connections, checksum errors — the receiver-side view of north-south traffic). To scrape these from your in-cluster Prometheus via the Prometheus Operator, enable the satellite ServiceMonitor:

```yaml
serviceMonitor:
  satelliteServiceMonitor:
    enabled: true
```

This creates a headless Service, Endpoints, and ServiceMonitor inside the cluster. The endpoint IPs are sourced from `northSouth.satellites[].host`, so your satellites must already be registered:

```yaml
northSouth:
  satellites:
    - name: satellite-01
      host: "72.61.23.103"    # this IP is used as the scrape target
```

The satellite ServiceMonitor is independent of `serviceMonitor.enabled` (which controls the orbit pod ServiceMonitor). You can add custom labels for Prometheus Operator discovery and annotations:

```yaml
serviceMonitor:
  satelliteServiceMonitor:
    enabled: true
    labels:
      release: kube-prometheus
    annotations: {}
```

## Stopping

```bash
docker compose -f satellite-compose.yaml down
```
