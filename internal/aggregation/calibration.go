package aggregation

// CalibrationTier indicates the quality of the reference baseline available
// for a model × hardware combination (spec §5.2, priority order).
type CalibrationTier string

const (
	TierAitraBenchmark CalibrationTier = "aitra_benchmark"
	TierReference      CalibrationTier = "reference"
	TierSelfCalibrated CalibrationTier = "self_calibrated"
	TierUncalibrated   CalibrationTier = "uncalibrated"
)

// CalibrationEntry holds a reference J/token value for a model × hardware pair.
type CalibrationEntry struct {
	Tier         CalibrationTier
	RefJPerToken float64 // 0 when tier == TierUncalibrated
}

// CalibrationKey identifies a model × hardware combination.
type CalibrationKey struct {
	Model    string
	Hardware string // GPU tier label, e.g. "h100", "l40s"
}

// CalibrationTable performs tier lookups against an in-process dataset.
// The dataset is loaded once at startup and is read-only afterwards,
// so CalibrationTable is safe for concurrent use.
type CalibrationTable struct {
	// rows is checked in priority order: aitra_benchmark > reference > self_calibrated.
	rows map[CalibrationKey]CalibrationEntry
}

// NewCalibrationTable builds a CalibrationTable from the provided entries.
// If two entries have the same key, the one with the higher-priority tier wins.
func NewCalibrationTable(entries []CalibrationEntry, keys []CalibrationKey) *CalibrationTable {
	t := &CalibrationTable{rows: make(map[CalibrationKey]CalibrationEntry, len(entries))}
	for i, e := range entries {
		k := keys[i]
		existing, ok := t.rows[k]
		if !ok || tierPriority(e.Tier) < tierPriority(existing.Tier) {
			t.rows[k] = e
		}
	}
	return t
}

// NewCalibrationTableFromMap is the convenience constructor for tests.
func NewCalibrationTableFromMap(m map[CalibrationKey]CalibrationEntry) *CalibrationTable {
	return &CalibrationTable{rows: m}
}

// Lookup returns the best available CalibrationEntry for the given model and
// hardware tier. If no entry exists, it returns (TierUncalibrated, 0).
func (t *CalibrationTable) Lookup(model, hardware string) CalibrationEntry {
	key := CalibrationKey{Model: model, Hardware: hardware}
	if e, ok := t.rows[key]; ok {
		return e
	}
	return CalibrationEntry{Tier: TierUncalibrated}
}

// tierPriority returns a sort key where lower = higher priority.
func tierPriority(t CalibrationTier) int {
	switch t {
	case TierAitraBenchmark:
		return 0
	case TierReference:
		return 1
	case TierSelfCalibrated:
		return 2
	default:
		return 3
	}
}
