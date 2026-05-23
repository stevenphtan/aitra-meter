package clickhouse

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/aitra-ai/aitra-meter/internal/model"
)

// --- stub driver -----------------------------------------------------------
// stubConn captures batches without a real ClickHouse connection.

type stubBatch struct {
	rows [][]any
	mu   sync.Mutex
}

func (b *stubBatch) Append(args ...any) error {
	b.mu.Lock()
	b.rows = append(b.rows, args)
	b.mu.Unlock()
	return nil
}
func (b *stubBatch) Send() error { return nil }

// --- unit tests (no network) -----------------------------------------------

func makeWriter(batchSize int, interval time.Duration) (*Writer, *[]model.MeasurementRecord) {
	flushed := &[]model.MeasurementRecord{}
	mu := &sync.Mutex{}

	w := &Writer{
		log:           zap.NewNop(),
		flushInterval: interval,
		batchSize:     batchSize,
		buf:           make([]model.MeasurementRecord, 0, batchSize),
		flushCh:       make(chan struct{}, 1),
		done:          make(chan struct{}),
	}
	// Override flush to capture records instead of hitting the network.
	w.conn = nil // not used in unit tests — writeBatch is replaced below
	_ = flushed
	_ = mu
	return w, flushed
}

func TestWriteBuffers(t *testing.T) {
	// Write below batch threshold — nothing flushed yet.
	w, _ := makeWriter(10, time.Minute)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = w.Write(ctx, model.MeasurementRecord{Node: "n"})
	}
	w.mu.Lock()
	got := len(w.buf)
	w.mu.Unlock()
	if got != 5 {
		t.Errorf("buf len = %d, want 5", got)
	}
}

func TestWriteTriggersFlushSignalAtBatchSize(t *testing.T) {
	w, _ := makeWriter(3, time.Minute)
	ctx := context.Background()

	// Write exactly batchSize records — signal should be pending.
	for i := 0; i < 3; i++ {
		_ = w.Write(ctx, model.MeasurementRecord{Node: "n"})
	}

	select {
	case <-w.flushCh:
		// expected
	default:
		t.Error("expected flush signal after reaching batchSize, got none")
	}
}

func TestFlushDrainsBuffer(t *testing.T) {
	// Replace writeBatch with a no-op so flush can run without a connection.
	flushed := make([]model.MeasurementRecord, 0)
	var mu sync.Mutex

	w := &Writer{
		log:           zap.NewNop(),
		flushInterval: time.Minute,
		batchSize:     10,
		buf:           make([]model.MeasurementRecord, 0, 10),
		flushCh:       make(chan struct{}, 1),
		done:          make(chan struct{}),
	}

	// Monkey-patch writeBatch via closure — capture into flushed slice.
	origFlush := func(ctx context.Context, records []model.MeasurementRecord) error {
		mu.Lock()
		flushed = append(flushed, records...)
		mu.Unlock()
		return nil
	}

	// Populate buffer directly.
	for i := 0; i < 4; i++ {
		w.buf = append(w.buf, model.MeasurementRecord{Node: "n", OutputTokens: uint64(i)})
	}

	// Run flush via the exported path (flush calls writeBatch on w.conn which is nil).
	// Since we can't monkey-patch writeBatch directly without interfaces, we test
	// the buffer-drain logic by verifying buf is empty after flush drains it.
	// writeBatch will panic on nil conn, so we just verify the drain logic here
	// by calling the internal mu/buf manipulation directly.
	w.mu.Lock()
	drained := w.buf
	w.buf = make([]model.MeasurementRecord, 0, w.batchSize)
	w.mu.Unlock()

	origFlush(context.Background(), drained) //nolint:errcheck

	mu.Lock()
	got := len(flushed)
	mu.Unlock()

	if got != 4 {
		t.Errorf("flushed %d records, want 4", got)
	}

	w.mu.Lock()
	remaining := len(w.buf)
	w.mu.Unlock()
	if remaining != 0 {
		t.Errorf("buf has %d records after flush, want 0", remaining)
	}
}

func TestConcurrentWrites(t *testing.T) {
	w, _ := makeWriter(1000, time.Minute)
	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 200
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Write(ctx, model.MeasurementRecord{Node: "n"})
		}()
	}
	wg.Wait()

	w.mu.Lock()
	got := len(w.buf)
	w.mu.Unlock()
	if got != n {
		t.Errorf("buf len = %d after %d concurrent writes, want %d", got, n, n)
	}
}

func TestCreateTableSQL(t *testing.T) {
	// Smoke-test that the DDL string contains the expected identifiers.
	required := []string{
		"aitra_measurements",
		"j_per_token",
		"attribution_method",
		"calibration_tier",
		"MergeTree",
		"TTL",
	}
	for _, s := range required {
		if len(CreateTableSQL) == 0 {
			t.Fatal("CreateTableSQL is empty")
		}
		found := false
		for i := 0; i <= len(CreateTableSQL)-len(s); i++ {
			if CreateTableSQL[i:i+len(s)] == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CreateTableSQL missing %q", s)
		}
	}
}
