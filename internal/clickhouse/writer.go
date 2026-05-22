// Package clickhouse provides a buffered async writer for measurement records.
package clickhouse

import (
	"context"
	"fmt"
	"sync"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/aitra-ai/aitra-meter/internal/aggregation"
)

const (
	// DefaultFlushInterval is how often the writer flushes to ClickHouse
	// regardless of batch size.
	DefaultFlushInterval = 5 * time.Second

	// DefaultBatchSize is the maximum number of records per batch.
	// A flush is triggered when this is reached before the interval.
	DefaultBatchSize = 500

	// CreateTableSQL is the DDL for the measurements table.
	// Engine: ReplacingMergeTree deduplicates on (cluster, node, window_id).
	CreateTableSQL = `
CREATE TABLE IF NOT EXISTS aitra_measurements (
    timestamp           DateTime64(3, 'UTC'),
    cluster             LowCardinality(String),
    node                LowCardinality(String),
    namespace           LowCardinality(String),
    workload            LowCardinality(String),
    model               LowCardinality(String),
    hardware            LowCardinality(String),
    precision           LowCardinality(String),
    team                LowCardinality(String),
    cost_centre         LowCardinality(String),
    energy_joules       Float64,
    output_tokens       UInt64,
    j_per_token         Float64,
    calibration_tier    LowCardinality(String),
    ref_j_per_token     Float64,
    attribution_method  LowCardinality(String),
    cv                  Float64,
    stable              Bool,
    energy_provider     LowCardinality(String),
    inference_provider  LowCardinality(String)
) ENGINE = MergeTree()
ORDER BY (cluster, namespace, model, timestamp)
PARTITION BY toYYYYMM(timestamp)
TTL timestamp + INTERVAL 90 DAY`
)

// Writer is a buffered async writer that flushes MeasurementRecords to
// ClickHouse either when the batch reaches BatchSize or after FlushInterval,
// whichever comes first. It is safe for concurrent use.
type Writer struct {
	conn          driver.Conn
	log           *zap.Logger
	flushInterval time.Duration
	batchSize     int

	mu      sync.Mutex
	buf     []aggregation.MeasurementRecord
	flushCh chan struct{}
	done    chan struct{}
	wg      sync.WaitGroup
}

// Config holds options for the Writer.
type Config struct {
	// DSN is the ClickHouse connection string, e.g.
	// "clickhouse://user:pass@host:9000/aitra?dial_timeout=5s"
	DSN string

	// FlushInterval overrides DefaultFlushInterval when > 0.
	FlushInterval time.Duration

	// BatchSize overrides DefaultBatchSize when > 0.
	BatchSize int
}

// New creates a Writer, applies the DDL to ensure the table exists,
// and starts the background flush goroutine.
func New(ctx context.Context, cfg Config, log *zap.Logger) (*Writer, error) {
	opts, err := ch.ParseDSN(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("clickhouse DSN: %w", err)
	}
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}
	if err := conn.Exec(ctx, CreateTableSQL); err != nil {
		return nil, fmt.Errorf("clickhouse create table: %w", err)
	}

	interval := cfg.FlushInterval
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	w := &Writer{
		conn:          conn,
		log:           log,
		flushInterval: interval,
		batchSize:     batchSize,
		buf:           make([]aggregation.MeasurementRecord, 0, batchSize),
		flushCh:       make(chan struct{}, 1),
		done:          make(chan struct{}),
	}
	w.wg.Add(1)
	go w.flushLoop()
	return w, nil
}

// Write buffers a record. If the buffer reaches BatchSize the flush goroutine
// is signalled immediately. Write never blocks on the network.
func (w *Writer) Write(_ context.Context, r aggregation.MeasurementRecord) error {
	w.mu.Lock()
	w.buf = append(w.buf, r)
	full := len(w.buf) >= w.batchSize
	w.mu.Unlock()

	if full {
		select {
		case w.flushCh <- struct{}{}:
		default: // signal already pending
		}
	}
	return nil
}

// Close flushes any remaining records and stops the background goroutine.
// It blocks until the final flush completes.
func (w *Writer) Close(ctx context.Context) error {
	close(w.done)
	w.wg.Wait()
	return w.flush(ctx)
}

// flushLoop runs in a goroutine and flushes on interval or signal.
func (w *Writer) flushLoop() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.flush(context.Background()) //nolint:errcheck
		case <-w.flushCh:
			w.flush(context.Background()) //nolint:errcheck
		case <-w.done:
			return
		}
	}
}

// flush drains the buffer and writes a batch to ClickHouse.
func (w *Writer) flush(ctx context.Context) error {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return nil
	}
	batch := w.buf
	w.buf = make([]aggregation.MeasurementRecord, 0, w.batchSize)
	w.mu.Unlock()

	if err := w.writeBatch(ctx, batch); err != nil {
		w.log.Error("clickhouse flush failed",
			zap.Int("records", len(batch)),
			zap.Error(err),
		)
		// Re-buffer on failure so records are not lost across a transient error.
		// On persistent failure the buffer will grow without bound — operators
		// should alert on clickhouse write errors.
		w.mu.Lock()
		w.buf = append(batch, w.buf...)
		w.mu.Unlock()
		return err
	}
	w.log.Debug("clickhouse flush ok", zap.Int("records", len(batch)))
	return nil
}

// writeBatch sends a batch of records using the native columnar protocol.
func (w *Writer) writeBatch(ctx context.Context, records []aggregation.MeasurementRecord) error {
	b, err := w.conn.PrepareBatch(ctx, `INSERT INTO aitra_measurements`)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}
	for _, r := range records {
		ts := time.UnixMilli(r.TimestampUnixMs).UTC()
		if err := b.Append(
			ts,
			r.Cluster, r.Node, r.Namespace, r.Workload, r.Model,
			r.Hardware, r.Precision, r.Team, r.CostCentre,
			r.EnergyJoules, r.OutputTokens, r.JPerToken,
			string(r.CalibrationTier), r.RefJPerToken,
			string(r.AttributionMethod),
			r.CV, r.Stable,
			r.EnergyProvider, r.InferenceProvider,
		); err != nil {
			return fmt.Errorf("append row: %w", err)
		}
	}
	return b.Send()
}
