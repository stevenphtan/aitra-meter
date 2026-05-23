# Changelog

All notable changes to Aitra Meter will be documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
Versioning follows [Semantic Versioning](https://semver.org/).

---

## [Unreleased]

### Added
- internal/agent/loop.go — measurement agent core loop (BeginWindow → EndWindow → token delta → gRPC ReportWindow)
- cmd/measurement-agent/main.go — fully wired entry point with --node, --energy-provider, --inference-provider, --aggregator, --window-seconds flags
- 7 race-clean tests in internal/agent/loop_test.go
- internal/model/record.go — zero-dependency MeasurementRecord, CalibrationTier, AttributionMethod types
- internal/storage/clickhouse/clickhouse.go — full Backend implementation with QueryChargeback
- internal/storage/memory/memory.go — in-memory backend for tests
- internal/storage/duckdb/duckdb.go — DuckDB backend stub (interface complete, driver TODO)
- ADR 0005: pluggable storage backend interface
- All 6 Phase 1 dashboard views (JPerTokenTable, TrendChart 2a/2b, ChargebackTable, IdleChart, CarbonCostTable)
- docker-compose dev stack + dev/seed.go (103k synthetic rows)

### Fixed
- Typo  →  in  (build error)
- NVML provider uses  which is Linux-only — added  tag to  and moved side-effect import to  (macOS build error)
- Literal newline inside string literal in  — replaced with  escape (build error)

### Added
- EnergyProvider and InferenceMetricsProvider pluggable interfaces (`internal/provider/`)
- Zeus energy provider stub
- vLLM inference provider stub
- generic-prometheus inference provider (compatible with TGI, SGLang, Ollama, Triton)
- Provider registry with `RegisterEnergy` / `RegisterInference` / `NewEnergy` / `NewInference`
- MeasurementPolicy CRD schema
- SiteConfig CRD schema
- Helm chart scaffold (`helm/aitra-meter/`)
- Technical specification v1.0 (`docs/spec/aitra-meter-spec-v1.0.md`)
- Architecture Decision Records 0001–0004
- SECURITY.md, ROADMAP.md, GOVERNANCE.md, CONTRIBUTING.md

### Fixed
- Typo `eneregyProviders` → `energyProviders` in `internal/provider/registry.go:38` caused `NewEnergy` to always return "unknown provider"
- Literal newline inside string literal in `internal/provider/inference/vllm/vllm.go:95` prevented compilation
- `github.com/NVIDIA/go-nvml` uses `RTLD_DEEPBIND` (Linux-only CGO constant); added `//go:build linux` to the nvml provider and split its side-effect import into `cmd/measurement-agent/providers_linux.go`

### Changed
- Annotation prefix migrated from `aitra.io` to `aitra-ai.github.io`
- API group migrated from `aitra.io/v1alpha1` to `aitra-ai.github.io/v1alpha1`

---

## Release cadence

Pre-v1.0: releases are tagged when a meaningful set of features is complete.  
Post-v1.0: minor releases monthly, patch releases as needed.
