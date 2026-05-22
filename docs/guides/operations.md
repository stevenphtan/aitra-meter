# Operations guide

## Upgrading

```bash
helm repo update
helm upgrade aitra-meter aitra/aitra-meter --namespace aitra-system
```

The measurement agent DaemonSet rolling-updates one node at a time. In-progress measurements on a node are abandoned (not corrupted) during a pod restart. The ClickHouse schema is backward-compatible across minor versions.

For major version upgrades, read the migration notes in [CHANGELOG.md](../../CHANGELOG.md) before upgrading.

---

## Resource sizing

### Measurement agent (per GPU node)

| Resource | Minimum | Recommended |
|---|---|---|
| CPU request | 100m | 200m |
| CPU limit | 500m | 500m |
| Memory request | 128Mi | 256Mi |
| Memory limit | 256Mi | 256Mi |

The agent's CPU usage scales with GPU count per node and sampling frequency. Default 10 Hz NVML sampling is within the minimum sizing above.

### Aggregation service

| Resource | Minimum | Recommended (10 nodes) |
|---|---|---|
| CPU request | 200m | 500m |
| CPU limit | 1000m | 2000m |
| Memory request | 256Mi | 512Mi |
| Memory limit | 512Mi | 1Gi |

Scales with number of nodes × models × namespaces (metric cardinality). For clusters with >50 GPU nodes, increase memory limit to 2Gi.

### ClickHouse

Default subchart sizing is appropriate for clusters with up to 50 nodes and 90-day retention. For larger clusters:

```yaml
clickhouse:
  persistence:
    size: 200Gi
```

Estimated storage: ~1 GB per node per month at default 15-second scrape interval.

---

## Log levels

All components log to stdout in JSON format. Set log level via Helm values:

```yaml
measurementAgent:
  logLevel: info    # debug | info | warn | error

aggregationService:
  logLevel: info
```

Debug logging is verbose — do not enable in production for more than a few minutes.

---

## Air-gapped installation

For clusters with no internet access:

1. Pull all images to a private registry:
```bash
docker pull ghcr.io/aitra-ai/aitra-meter/measurement-agent:0.1.0
docker pull ghcr.io/aitra-ai/aitra-meter/aggregation-service:0.1.0
docker pull ghcr.io/aitra-ai/aitra-meter/dashboard:0.1.0
# Tag and push to your private registry
```

2. Install with air-gapped mode and private registry:
```bash
helm install aitra-meter aitra/aitra-meter   --namespace aitra-system   --create-namespace   --set airGapped.enabled=true   --set measurementAgent.image.repository=my-registry/aitra-meter/measurement-agent   --set aggregationService.image.repository=my-registry/aitra-meter/aggregation-service   --set dashboard.image.repository=my-registry/aitra-meter/dashboard   --set siteConfig.carbonIntensityFallback=400   --set siteConfig.electricityCostPerKwh=0.12
```

Air-gapped mode disables all outbound API calls (ElectricityMaps, WattTime, OpenEI). Carbon and cost conversions use the manual SiteConfig values.

---

## Scaling

Aitra Meter Phase 1 runs one aggregation service replica. Horizontal scaling is not supported in Phase 1 — the aggregation service maintains in-memory rolling state for CV gate calculations. Phase 2 will introduce a distributed state store.

The measurement agent DaemonSet scales automatically with the cluster node count.

---

## Backup and restore

ClickHouse data should be backed up if historical chargeback records are required for compliance. Use ClickHouse's native backup:

```bash
clickhouse-client --query "BACKUP TABLE aitra_measurements TO Disk('backups', 'aitra_$(date +%Y%m%d).zip')"
```

Prometheus data (hot store, 15-day retention by default) is ephemeral and does not require backup.

---

## Disabling components

Disable the dashboard if you are using Grafana instead:
```yaml
dashboard:
  enabled: false
```

Disable ClickHouse if you have an external instance:
```yaml
clickhouse:
  enabled: false
  external:
    host: my-clickhouse.internal
    port: 9000
    database: aitra
```

Disable the Prometheus ServiceMonitor if you manage scrape configs manually:
```yaml
prometheus:
  serviceMonitor:
    enabled: false
```

---

## Uninstalling

```bash
helm uninstall aitra-meter --namespace aitra-system

# Remove CRDs (optional — only if you are done with Aitra Meter entirely)
kubectl delete crd measurementpolicies.aitra-ai.github.io
kubectl delete crd siteconfigs.aitra-ai.github.io

# Remove namespace
kubectl delete namespace aitra-system
```

CRDs are not removed by `helm uninstall` by default. Remove them manually only if you are certain no other system references them.
