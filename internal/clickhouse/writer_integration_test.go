//go:build integration

package clickhouse

import (
	"context"
	"fmt"
	"testing"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/aitra-ai/aitra-meter/internal/aggregation"
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
	defer w.Close(ctx) //nolint:errcheck

	// Write 10 records.
	for i := 0; i < 10; i++ {
		_ = w.Write(ctx, aggregation.MeasurementRecord{
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
			CalibrationTier:   aggregation.TierAitraBenchmark,
			RefJPerToken:      0.31,
			AttributionMethod: aggregation.AttributionDirect,
			CV:                0.01,
			Stable:            true,
			EnergyProvider:    "nvml",
			InferenceProvider: "vllm",
		})
	}

	// Wait for flush interval + margin.
	time.Sleep(1 * time.Second)

	// Query the table.
	conn, err := chdriver.Open(&chdriver.Options{
		Addr: []string{fmt.Sprintf("%s", dsn)},
	})
	_ = conn
	// Use a raw query via the Writer's connection instead.
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
	defer w.Close(ctx) //nolint:errcheck

	for i := 0; i < 5; i++ {
		_ = w.Write(ctx, aggregation.MeasurementRecord{
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
