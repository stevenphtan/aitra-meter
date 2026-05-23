//go:build integration

package clickhouse

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/aitra-ai/aitra-meter/internal/model"
)

func startClickHouse(ctx context.Context, t *testing.T) (dsn string, stop func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server:24.3-alpine",
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"CLICKHOUSE_DB":       "aitra",
			"CLICKHOUSE_USER":     "default",
			"CLICKHOUSE_PASSWORD": "",
		},
		WaitingFor: wait.ForLog("Ready for connections").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start clickhouse container: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	dsn = fmt.Sprintf("clickhouse://default:@%s:%s/aitra?dial_timeout=5s", host, port.Port())
	stop = func() { _ = c.Terminate(ctx) }
	return
}

func TestWriterIntegration(t *testing.T) {
	ctx := context.Background()
	dsn, stop := startClickHouse(ctx, t)
	defer stop()

	w, err := New(ctx, Config{
		DSN:           dsn,
		FlushInterval: 500 * time.Millisecond,
		BatchSize:     50,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	// Write 10 records.
	for i := 0; i < 10; i++ {
		_ = w.Write(ctx, model.MeasurementRecord{
			TimestampUnixMs:   time.Now().UnixMilli(),
			Cluster:           "test-cluster",
			Node:              fmt.Sprintf("node-%d", i%3),
			Namespace:         "prod",
			Workload:          "chat",
			Model:             "llama-3-8b",
			Hardware:          "h100",
			Precision:         "fp16",
			EnergyJoules:      412.4,
			OutputTokens:      1328,
			JPerToken:         0.3105,
			CalibrationTier:   model.TierAitraBenchmark,
			RefJPerToken:      0.31,
			AttributionMethod: model.AttributionDirect,
			CV:                0.01,
			Stable:            true,
			EnergyProvider:    "nvml",
			InferenceProvider: "vllm",
		})
	}

	// Wait for flush interval + margin.
	time.Sleep(1 * time.Second)

		rows, err := w.conn.Query(ctx, `SELECT count() FROM aitra_measurements`)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	defer rows.Close()
	var count uint64
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	if count != 10 {
		t.Errorf("row count = %d, want 10", count)
	}
}

// TestChargebackQuery30Day seeds 30 days of synthetic data across 3 namespaces
// and asserts that the chargeback GROUP BY query (used by View 3) completes
// within 10 seconds — AC-11.
//
// Row count: 3 namespaces × 2 models × 8,640 windows (5-min interval, 30 days)
// = 51,840 rows. The MergeTree ORDER BY (cluster, namespace, model, timestamp)
// makes this query highly selective.
func TestChargebackQuery30Day(t *testing.T) {
	ctx := context.Background()
	dsn, stop := startClickHouse(ctx, t)
	defer stop()

	w, err := New(ctx, Config{
		DSN:           dsn,
		FlushInterval: 500 * time.Millisecond,
		BatchSize:     500,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	type combo struct {
		namespace string
		model     string
		method    string
	}
	combos := []combo{
		{"prod", "llama-3-8b", "direct"},
		{"prod", "llama-3-70b", "direct"},
		{"staging", "llama-3-8b", "direct"},
		{"staging", "llama-3-70b", "direct"},
		{"shared", "llama-3-8b", "proportional"},
		{"shared", "llama-3-70b", "proportional"},
	}

	// One record every 5 minutes for 30 days = 8,640 records per combo.
	step := 5 * time.Minute
	end := time.Now().UTC().Truncate(step)
	start := end.Add(-30 * 24 * time.Hour)

	totalInserted := 0
	for _, c := range combos {
		for ts := start; ts.Before(end); ts = ts.Add(step) {
			_ = w.Write(ctx, model.MeasurementRecord{
				TimestampUnixMs:   ts.UnixMilli(),
				Cluster:           "test",
				Node:              "node-0",
				Namespace:         c.namespace,
				Workload:          "chat",
				Model:             c.model,
				Hardware:          "h100",
				Precision:         "fp16",
				EnergyJoules:      412.0,
				OutputTokens:      1000,
				JPerToken:         0.412,
				CalibrationTier:   model.TierAitraBenchmark,
				AttributionMethod: model.AttributionMethod(c.method),
				CV:                0.01,
				Stable:            true,
				EnergyProvider:    "nvml",
				InferenceProvider: "vllm",
			})
			totalInserted++
		}
		// Flush after each combo to avoid holding too much in memory.
		time.Sleep(600 * time.Millisecond)
	}

	// Final flush + small margin for the last batch.
	time.Sleep(1 * time.Second)

	t.Logf("inserted %d rows", totalInserted)

	// --- AC-11: 30-day chargeback query must complete within 10 seconds ---
	chargebackSQL := `
		SELECT
			namespace,
			workload,
			model,
			hardware,
			any(calibration_tier)   AS calibration_tier,
			any(attribution_method) AS attribution_method,
			SUM(energy_joules)      AS energy_joules,
			SUM(output_tokens)      AS token_count
		FROM aitra_measurements
		WHERE timestamp >= now() - INTERVAL 30 DAY
		GROUP BY namespace, workload, model, hardware
		ORDER BY energy_joules DESC`

	queryStart := time.Now()
	rows, err := w.conn.Query(ctx, chargebackSQL)
	if err != nil {
		t.Fatalf("chargeback query: %v", err)
	}
	defer rows.Close()

	var rowCount int
	for rows.Next() {
		var ns, wl, model, hw, tier, method string
		var joules float64
		var tokens uint64
		if err := rows.Scan(&ns, &wl, &model, &hw, &tier, &method, &joules, &tokens); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rowCount++
	}
	elapsed := time.Since(queryStart)

	t.Logf("chargeback query returned %d rows in %s", rowCount, elapsed)

	if rowCount != len(combos) {
		t.Errorf("chargeback query returned %d rows, want %d", rowCount, len(combos))
	}
	if elapsed > 10*time.Second {
		t.Errorf("chargeback query took %s, must complete within 10s (AC-11)", elapsed)
	}
}

func TestWriterBatchFlushOnSize(t *testing.T) {
	ctx := context.Background()
	dsn, stop := startClickHouse(ctx, t)
	defer stop()

	// Large interval so only a size-triggered flush fires.
	w, err := New(ctx, Config{
		DSN:           dsn,
		FlushInterval: 10 * time.Minute,
		BatchSize:     5,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	for i := 0; i < 5; i++ {
		_ = w.Write(ctx, model.MeasurementRecord{
			TimestampUnixMs: time.Now().UnixMilli(),
			Cluster:         "test",
			OutputTokens:    uint64(i + 1),
			JPerToken:       0.3,
		})
	}

	// Give the flush goroutine a moment to process the signal.
	time.Sleep(200 * time.Millisecond)

	rows, err := w.conn.Query(ctx, `SELECT count() FROM aitra_measurements`)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	defer rows.Close()
	var count uint64
	if rows.Next() {
		_ = rows.Scan(&count)
	}
	if count != 5 {
		t.Errorf("row count = %d after size-triggered flush, want 5", count)
	}
}
