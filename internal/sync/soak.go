package sync

// Soak support for the long-running follow check. The nightly integration
// job follows testnet for 30 minutes, which is too short to show a slow
// memory leak or slow lag drift, so the same test can be run for longer
// with thresholds that fail the run. Everything here is network-free and
// unit-tested under the default build tag; only the caller in
// integration_test.go touches testnet.
//
// A 24-hour soak cannot be one GitHub Actions job: a job on a
// GitHub-hosted runner is killed at 6 hours. The soak therefore runs as a
// chain of shorter segments that carry the index and the samples forward,
// and every sample records which segment (that is, which process) produced
// it. Lag is judged across the whole chain, because it is a property of
// the index against the network. Memory growth is judged inside one
// segment only, because each segment is a fresh process: the resident-set
// series is discontinuous at a segment boundary and comparing across one
// would compare two different processes' startup states.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// Soak defaults. DefaultSoakDuration is 30 minutes so that the nightly
// integration job keeps its Stage F behavior when no soak variable is set.
const (
	DefaultSoakDuration       = 30 * time.Minute
	DefaultSoakSampleInterval = time.Minute
	// DefaultSoakMaxLag is about two minutes of ledgers at five seconds
	// each: far above the lag a healthy follow loop shows, and low enough
	// that a stalled writer fails the soak.
	DefaultSoakMaxLag = 25
	// DefaultSoakMaxGrowthBytes and DefaultSoakMaxGrowthRatio are starting
	// points, not measurements. No 24-hour soak has run yet, so there is
	// no measured plateau to set them from; tighten them once a real soak
	// reports its per-segment growth (MAINTAINING.md).
	DefaultSoakMaxGrowthBytes = 256 << 20 // 256 MiB
	DefaultSoakMaxGrowthRatio = 2.0
	// MinSoakGrowthSamples is the fewest samples a segment needs before
	// its memory trend is reported as a verdict instead of inconclusive.
	MinSoakGrowthSamples = 6
)

// Environment variables that configure a soak run. They are read by
// ParseSoakConfig and carry the SEP47IDX_ prefix like every other setting.
const (
	EnvSoakDuration       = config.EnvPrefix + "SOAK_DURATION"
	EnvSoakSampleInterval = config.EnvPrefix + "SOAK_SAMPLE_INTERVAL"
	EnvSoakMaxLag         = config.EnvPrefix + "SOAK_MAX_LAG"
	EnvSoakMaxGrowthBytes = config.EnvPrefix + "SOAK_MAX_MEMORY_GROWTH_BYTES"
	EnvSoakMaxGrowthRatio = config.EnvPrefix + "SOAK_MAX_MEMORY_GROWTH_RATIO"
	EnvSoakSegment        = config.EnvPrefix + "SOAK_SEGMENT"
	EnvSoakSampleDir      = config.EnvPrefix + "SOAK_SAMPLE_DIR"
	EnvSoakDB             = config.EnvPrefix + "SOAK_DB"
	EnvSoakReport         = config.EnvPrefix + "SOAK_REPORT"
	// EnvSoakEnforce turns the lag and memory thresholds from reported
	// information into test failures. It is off unless set, so the nightly
	// 30-minute integration job keeps exactly the pass/fail conditions it
	// had before the soak existed; the soak workflow sets it to true.
	EnvSoakEnforce = config.EnvPrefix + "SOAK_ENFORCE"
)

// ErrSoakThreshold means a soak run violated a configured threshold. It is
// a single sentinel so a caller can tell a threshold failure from an RPC or
// database failure without reading messages.
var ErrSoakThreshold = errors.New("soak threshold violated")

// SoakDatabaseState is the durable position that a segment resumes. The
// checkpoint must name the same ledger as last_ledger; its hash is what Loop
// compares with testnet before the first write in a resumed segment.
type SoakDatabaseState struct {
	LastLedger uint32
	Checkpoint store.Checkpoint
}

// CheckSoakDatabase verifies SQLite's own consistency check and the sync
// position invariant needed to carry an index between soak segments. A new
// database has neither last_ledger nor a checkpoint and returns the zero state.
func CheckSoakDatabase(ctx context.Context, s *store.Store) (SoakDatabaseState, error) {
	var integrity string
	if err := s.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return SoakDatabaseState{}, fmt.Errorf("soak database integrity check: %w", err)
	}
	if integrity != "ok" {
		return SoakDatabaseState{}, fmt.Errorf("soak database integrity check: %s", integrity)
	}
	lastText, err := s.State(ctx, store.KeyLastLedger)
	if errors.Is(err, store.ErrNotFound) {
		if _, checkpointErr := s.Checkpoint(ctx); !errors.Is(checkpointErr, store.ErrNotFound) {
			return SoakDatabaseState{}, fmt.Errorf("soak database has a checkpoint without last_ledger: %v", checkpointErr)
		}
		return SoakDatabaseState{}, nil
	}
	if err != nil {
		return SoakDatabaseState{}, fmt.Errorf("soak database last_ledger: %w", err)
	}
	last, err := strconv.ParseUint(lastText, 10, 32)
	if err != nil || last == 0 {
		return SoakDatabaseState{}, fmt.Errorf("soak database last_ledger %q is invalid", lastText)
	}
	checkpoint, err := s.Checkpoint(ctx)
	if err != nil {
		return SoakDatabaseState{}, fmt.Errorf("soak database checkpoint: %w", err)
	}
	if checkpoint.Ledger != uint32(last) {
		return SoakDatabaseState{}, fmt.Errorf("soak database checkpoint ledger %d differs from last_ledger %d", checkpoint.Ledger, last)
	}
	return SoakDatabaseState{LastLedger: uint32(last), Checkpoint: checkpoint}, nil
}

// SoakConfig holds the duration and thresholds of one soak segment. The
// zero value is not usable; ParseSoakConfig fills the defaults.
type SoakConfig struct {
	// Duration is how long this segment follows the tip.
	Duration time.Duration
	// SampleInterval is the shortest gap between two recorded samples.
	SampleInterval time.Duration
	// MaxLag fails the run when a post-catch-up sample is further behind
	// the RPC tip than this, in ledgers.
	MaxLag uint32
	// MaxGrowthBytes and MaxGrowthRatio fail the run when memory in one
	// segment grows by more than either limit. Each applies on its own,
	// so a large absolute rise fails even at a small ratio.
	MaxGrowthBytes uint64
	MaxGrowthRatio float64
	// Segment numbers this process in the chain, from 1.
	Segment int
	// SampleDir holds one JSON Lines file per segment. Empty keeps the
	// samples in memory only, which is what a local run wants.
	SampleDir string
	// DBPath is the index to create or resume. Empty means the caller
	// picks a throwaway path, which keeps the nightly job stateless.
	DBPath string
	// ReportPath receives the Markdown report. Empty prints it only.
	ReportPath string
	// Enforce makes a threshold violation fail the run. False, the
	// default, records and reports the same samples and verdicts without
	// failing anything: that is the nightly job's mode, and it is why
	// adding the soak did not add a way for the nightly follow to go red.
	// A soak run sets it explicitly.
	Enforce bool
}

// ParseSoakConfig reads the soak settings from lookup, which is
// os.LookupEnv in the integration test and a map in unit tests. An unset or
// empty variable keeps its default, so the nightly 30-minute job needs no
// configuration at all.
func ParseSoakConfig(lookup func(string) (string, bool)) (SoakConfig, error) {
	c := SoakConfig{
		Duration:       DefaultSoakDuration,
		SampleInterval: DefaultSoakSampleInterval,
		MaxLag:         DefaultSoakMaxLag,
		MaxGrowthBytes: DefaultSoakMaxGrowthBytes,
		MaxGrowthRatio: DefaultSoakMaxGrowthRatio,
		Segment:        1,
	}
	var err error
	if c.Duration, err = soakDuration(lookup, EnvSoakDuration, c.Duration); err != nil {
		return SoakConfig{}, err
	}
	if c.SampleInterval, err = soakDuration(lookup, EnvSoakSampleInterval, c.SampleInterval); err != nil {
		return SoakConfig{}, err
	}
	if v, ok := soakValue(lookup, EnvSoakMaxLag); ok {
		n, perr := strconv.ParseUint(v, 10, 32)
		if perr != nil {
			return SoakConfig{}, fmt.Errorf("%s=%q: %w", EnvSoakMaxLag, v, perr)
		}
		c.MaxLag = uint32(n)
	}
	if v, ok := soakValue(lookup, EnvSoakMaxGrowthBytes); ok {
		n, perr := strconv.ParseUint(v, 10, 64)
		if perr != nil {
			return SoakConfig{}, fmt.Errorf("%s=%q: %w", EnvSoakMaxGrowthBytes, v, perr)
		}
		c.MaxGrowthBytes = n
	}
	if v, ok := soakValue(lookup, EnvSoakMaxGrowthRatio); ok {
		f, perr := strconv.ParseFloat(v, 64)
		if perr != nil {
			return SoakConfig{}, fmt.Errorf("%s=%q: %w", EnvSoakMaxGrowthRatio, v, perr)
		}
		if f < 1 {
			return SoakConfig{}, fmt.Errorf("%s=%q: ratio must be at least 1", EnvSoakMaxGrowthRatio, v)
		}
		c.MaxGrowthRatio = f
	}
	if v, ok := soakValue(lookup, EnvSoakSegment); ok {
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return SoakConfig{}, fmt.Errorf("%s=%q: %w", EnvSoakSegment, v, perr)
		}
		if n < 1 {
			return SoakConfig{}, fmt.Errorf("%s=%q: segments are numbered from 1", EnvSoakSegment, v)
		}
		c.Segment = n
	}
	if v, ok := soakValue(lookup, EnvSoakEnforce); ok {
		b, perr := strconv.ParseBool(v)
		if perr != nil {
			return SoakConfig{}, fmt.Errorf("%s=%q: want a boolean such as true or false: %w", EnvSoakEnforce, v, perr)
		}
		c.Enforce = b
	}
	c.SampleDir, _ = soakValue(lookup, EnvSoakSampleDir)
	c.DBPath, _ = soakValue(lookup, EnvSoakDB)
	c.ReportPath, _ = soakValue(lookup, EnvSoakReport)
	if c.SampleInterval > c.Duration {
		return SoakConfig{}, fmt.Errorf("%s (%s) must not exceed %s (%s)", EnvSoakSampleInterval, c.SampleInterval, EnvSoakDuration, c.Duration)
	}
	return c, nil
}

func soakValue(lookup func(string) (string, bool), name string) (string, bool) {
	if lookup == nil {
		return "", false
	}
	v, ok := lookup(name)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func soakDuration(lookup func(string) (string, bool), name string, def time.Duration) (time.Duration, error) {
	v, ok := soakValue(lookup, name)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", name, v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s=%q: must be positive", name, v)
	}
	return d, nil
}

// SoakMemory is one memory observation. RSSBytes is the process's resident
// set size, which is what a container or runner limit applies to;
// HeapBytes is the Go heap in use, recorded as a fallback for platforms
// with no procfs so the growth check still works there.
type SoakMemory struct {
	RSSBytes  uint64
	HeapBytes uint64
}

// ReadSoakMemory reads the resident set size from /proc/self/statm and the
// Go heap from the runtime. A missing or unreadable procfs is not an error:
// RSSBytes stays 0 and the sampler falls back to the heap figure, because a
// soak that reports heap growth is more useful than one that fails to start.
func ReadSoakMemory() SoakMemory {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m := SoakMemory{HeapBytes: ms.HeapInuse}
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return m
	}
	// statm is "size resident shared ..." in pages.
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return m
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return m
	}
	size := os.Getpagesize()
	if size <= 0 {
		return m
	}
	m.RSSBytes = pages * uint64(size)
	return m
}

// SoakSample is one timestamped observation of follow progress and process
// memory. Samples are written as JSON Lines so a failed segment still
// leaves every sample it managed to record.
type SoakSample struct {
	At             time.Time `json:"at"`
	Segment        int       `json:"segment"`
	ElapsedSeconds float64   `json:"elapsed_seconds"`
	LastLedger     uint32    `json:"last_ledger"`
	LatestLedger   uint32    `json:"latest_ledger"`
	Lag            uint32    `json:"lag"`
	// CaughtUp is true once the loop has reported caught_up at least once
	// in this segment. Lag before that is the backlog being worked off,
	// not drift, so the lag threshold ignores those samples.
	CaughtUp bool `json:"caught_up"`
	// MemoryBytes is RSSBytes where procfs is available and HeapBytes
	// otherwise; MemorySource says which, so a report never implies it
	// measured RSS when it measured the heap.
	MemoryBytes  uint64 `json:"memory_bytes"`
	MemorySource string `json:"memory_source"`
	RSSBytes     uint64 `json:"rss_bytes"`
	HeapBytes    uint64 `json:"heap_in_use_bytes"`
}

// SoakSampler records samples at most once per SoakConfig.SampleInterval
// and appends each one to this segment's JSON Lines file.
type SoakSampler struct {
	// Now and Mem are injection points for tests; nil means the real clock
	// and the real process.
	Now func() time.Time
	Mem func() SoakMemory

	cfg      SoakConfig
	mu       sync.Mutex
	start    time.Time
	lastAt   time.Time
	haveLast bool
	caughtUp bool
	samples  []SoakSample
	file     *os.File
	writeErr error
}

// SoakSampleFile is where segment n writes its samples under dir. One file
// per segment means segments never overwrite each other's data when their
// artifacts are merged.
func SoakSampleFile(dir string, segment int) string {
	return filepath.Join(dir, fmt.Sprintf("segment-%03d.jsonl", segment))
}

// NewSoakSampler creates the sampler for one segment. With a SampleDir it
// truncates this segment's own file, so rerunning a segment replaces its
// samples instead of appending a second copy, and leaves other segments'
// files untouched.
func NewSoakSampler(cfg SoakConfig) (*SoakSampler, error) {
	s := &SoakSampler{cfg: cfg}
	if cfg.SampleDir == "" {
		return s, nil
	}
	if err := os.MkdirAll(cfg.SampleDir, 0o750); err != nil {
		return nil, fmt.Errorf("create sample dir %s: %w", cfg.SampleDir, err)
	}
	path := SoakSampleFile(cfg.SampleDir, cfg.Segment)
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open sample file %s: %w", path, err)
	}
	s.file = f
	return s, nil
}

func (s *SoakSampler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *SoakSampler) mem() SoakMemory {
	if s.Mem != nil {
		return s.Mem()
	}
	return ReadSoakMemory()
}

// Observe records a sample when SampleInterval has passed since the last
// one, and reports whether it did. It is meant for Loop.Progress, which
// fires on every batch and every caught-up poll; the interval keeps a
// 24-hour run to a few thousand samples.
func (s *SoakSampler) Observe(last, latest uint32, caughtUp bool) (SoakSample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The clock is read once per observation, so a test clock that
	// advances on every read steps predictably.
	now := s.now()
	if s.haveLast && now.Sub(s.lastAt) < s.cfg.SampleInterval {
		s.caughtUp = s.caughtUp || caughtUp
		return SoakSample{}, false
	}
	return s.record(now, last, latest, caughtUp), true
}

// Record always records a sample. The caller uses it for the final
// observation, which must appear whatever the interval says.
func (s *SoakSampler) Record(last, latest uint32, caughtUp bool) SoakSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.record(s.now(), last, latest, caughtUp)
}

func (s *SoakSampler) record(now time.Time, last, latest uint32, caughtUp bool) SoakSample {
	if s.start.IsZero() {
		s.start = now
	}
	if caughtUp {
		s.caughtUp = true
	}
	m := s.mem()
	sample := SoakSample{
		At:             now.UTC(),
		Segment:        s.cfg.Segment,
		ElapsedSeconds: now.Sub(s.start).Seconds(),
		LastLedger:     last,
		LatestLedger:   latest,
		CaughtUp:       s.caughtUp,
		RSSBytes:       m.RSSBytes,
		HeapBytes:      m.HeapBytes,
	}
	if latest > last {
		sample.Lag = latest - last
	}
	sample.MemoryBytes, sample.MemorySource = m.RSSBytes, "rss"
	if m.RSSBytes == 0 {
		sample.MemoryBytes, sample.MemorySource = m.HeapBytes, "heap"
	}
	s.samples = append(s.samples, sample)
	s.lastAt, s.haveLast = now, true
	if s.file != nil && s.writeErr == nil {
		line, err := json.Marshal(sample)
		if err != nil {
			s.writeErr = fmt.Errorf("encode sample: %w", err)
			return sample
		}
		if _, err := s.file.Write(append(line, '\n')); err != nil {
			s.writeErr = fmt.Errorf("write sample: %w", err)
		}
	}
	return sample
}

// Samples returns a copy of what this segment recorded, in order.
func (s *SoakSampler) Samples() []SoakSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.samples)
}

// Close flushes and closes the sample file and reports the first write
// error, so a soak never passes while silently failing to record evidence.
func (s *SoakSampler) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		if err := s.file.Close(); err != nil && s.writeErr == nil {
			s.writeErr = fmt.Errorf("close sample file: %w", err)
		}
		s.file = nil
	}
	return s.writeErr
}

// LoadSoakSamples reads every segment file in dir, oldest segment first. A
// missing directory yields no samples and no error, which is the first
// segment's normal state.
func LoadSoakSamples(dir string) ([]SoakSample, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "segment-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("list sample files in %s: %w", dir, err)
	}
	slices.Sort(paths)
	var out []SoakSample
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var s SoakSample
			if err := json.Unmarshal([]byte(line), &s); err != nil {
				return nil, fmt.Errorf("%s line %d: %w", p, i+1, err)
			}
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b SoakSample) int {
		if a.Segment != b.Segment {
			return a.Segment - b.Segment
		}
		return a.At.Compare(b.At)
	})
	return out, nil
}

// SoakSegmentStats summarizes one segment, that is one process. Memory
// figures are medians of the segment's first and last third, which ignores
// single-sample spikes without smoothing away a steady rise.
type SoakSegmentStats struct {
	Segment          int
	Samples          int
	CaughtUpSamples  int
	Start, End       time.Time
	Span             time.Duration
	MaxLag           uint32
	MedianLag        uint32
	LagViolations    int
	MemorySource     string
	FirstMemoryBytes uint64
	LastMemoryBytes  uint64
	GrowthBytes      int64
	GrowthRatio      float64
	// GrowthVerdict is "ok", "violation", or "inconclusive" when the
	// segment has fewer than MinSoakGrowthSamples samples.
	GrowthVerdict string
}

// SoakReport is the result of evaluating a whole soak chain.
type SoakReport struct {
	Config          SoakConfig
	Samples         int
	CaughtUpSamples int
	Segments        []SoakSegmentStats
	Start, End      time.Time
	Span            time.Duration
	MaxLag          uint32
	MedianLag       uint32
	LagViolations   int
	WorstLag        SoakSample
	// Violations are the human-readable threshold failures. Empty means
	// the run met every configured threshold.
	Violations []string
}

// EvaluateSoak judges a sample series against cfg. Lag is judged across
// every segment, because it measures the index against the network. Memory
// growth is judged within each segment only: segments are separate
// processes, so a cross-segment comparison would measure process startup,
// not a leak.
func EvaluateSoak(samples []SoakSample, cfg SoakConfig) SoakReport {
	r := SoakReport{Config: cfg, Samples: len(samples)}
	if len(samples) == 0 {
		r.Violations = append(r.Violations, "no samples were recorded")
		return r
	}
	sorted := slices.Clone(samples)
	slices.SortStableFunc(sorted, func(a, b SoakSample) int {
		if a.Segment != b.Segment {
			return a.Segment - b.Segment
		}
		return a.At.Compare(b.At)
	})
	r.Start, r.End = sorted[0].At, sorted[len(sorted)-1].At
	r.Span = r.End.Sub(r.Start)

	var allLags []uint32
	bySegment := map[int][]SoakSample{}
	var order []int
	for _, s := range sorted {
		if _, ok := bySegment[s.Segment]; !ok {
			order = append(order, s.Segment)
		}
		bySegment[s.Segment] = append(bySegment[s.Segment], s)
		if !s.CaughtUp {
			continue
		}
		r.CaughtUpSamples++
		allLags = append(allLags, s.Lag)
		if s.Lag > r.MaxLag {
			r.MaxLag, r.WorstLag = s.Lag, s
		}
		if s.Lag > cfg.MaxLag {
			r.LagViolations++
		}
	}
	r.MedianLag = medianUint32(allLags)

	for _, n := range order {
		r.Segments = append(r.Segments, evaluateSoakSegment(n, bySegment[n], cfg))
	}
	if r.CaughtUpSamples == 0 {
		r.Violations = append(r.Violations, fmt.Sprintf("the index never caught up to the tip in %d samples", len(sorted)))
	}
	if r.LagViolations > 0 {
		r.Violations = append(r.Violations, fmt.Sprintf("lag exceeded %d ledgers in %d of %d samples after catch-up (worst %d at ledger %d, %s)",
			cfg.MaxLag, r.LagViolations, r.CaughtUpSamples, r.WorstLag.Lag, r.WorstLag.LastLedger, r.WorstLag.At.Format(time.RFC3339)))
	}
	for _, seg := range r.Segments {
		if seg.GrowthVerdict != "violation" {
			continue
		}
		r.Violations = append(r.Violations, fmt.Sprintf("segment %d memory (%s) grew from %d to %d bytes (+%d, x%.2f) over %s, above the limit of %d bytes or x%.2f",
			seg.Segment, seg.MemorySource, seg.FirstMemoryBytes, seg.LastMemoryBytes, seg.GrowthBytes, seg.GrowthRatio, seg.Span.Round(time.Second), cfg.MaxGrowthBytes, cfg.MaxGrowthRatio))
	}
	return r
}

func evaluateSoakSegment(n int, samples []SoakSample, cfg SoakConfig) SoakSegmentStats {
	st := SoakSegmentStats{Segment: n, Samples: len(samples), GrowthVerdict: "inconclusive"}
	if len(samples) == 0 {
		return st
	}
	st.Start, st.End = samples[0].At, samples[len(samples)-1].At
	st.Span = st.End.Sub(st.Start)
	st.MemorySource = samples[len(samples)-1].MemorySource
	var lags []uint32
	for _, s := range samples {
		if !s.CaughtUp {
			continue
		}
		st.CaughtUpSamples++
		lags = append(lags, s.Lag)
		if s.Lag > st.MaxLag {
			st.MaxLag = s.Lag
		}
		if s.Lag > cfg.MaxLag {
			st.LagViolations++
		}
	}
	st.MedianLag = medianUint32(lags)
	if len(samples) < MinSoakGrowthSamples {
		return st
	}
	third := len(samples) / 3
	if third < 1 {
		third = 1
	}
	first := make([]uint64, 0, third)
	for _, s := range samples[:third] {
		first = append(first, s.MemoryBytes)
	}
	lastPart := make([]uint64, 0, third)
	for _, s := range samples[len(samples)-third:] {
		lastPart = append(lastPart, s.MemoryBytes)
	}
	st.FirstMemoryBytes, st.LastMemoryBytes = medianUint64(first), medianUint64(lastPart)
	st.GrowthBytes = soakGrowth(st.FirstMemoryBytes, st.LastMemoryBytes)
	if st.FirstMemoryBytes > 0 {
		st.GrowthRatio = float64(st.LastMemoryBytes) / float64(st.FirstMemoryBytes)
	}
	st.GrowthVerdict = "ok"
	if st.GrowthBytes > 0 && uint64(st.GrowthBytes) > cfg.MaxGrowthBytes {
		st.GrowthVerdict = "violation"
	}
	// The ratio qualifies the absolute budget; it is not a trigger of its
	// own. A segment starts with a cold process, so its first third includes
	// warm-up: a 40 MiB resident set reaching 90 MiB doubles without leaking
	// anything, and an independent ratio test would fail healthy runs.
	// Growth must also pass soakRatioFloor before the ratio can condemn it.
	if st.GrowthBytes > 0 && uint64(st.GrowthBytes) > soakRatioFloor(cfg) && st.GrowthRatio > cfg.MaxGrowthRatio {
		st.GrowthVerdict = "violation"
	}
	return st
}

// soakRatioFloor is the absolute growth a segment must show before its
// growth ratio counts. It is a quarter of the configured byte budget rather
// than a separate knob, so tightening MaxGrowthBytes after a real soak has
// measured the steady state tightens the ratio test with it.
func soakRatioFloor(cfg SoakConfig) uint64 { return cfg.MaxGrowthBytes / 4 }

// soakGrowth returns last-first as a signed difference. A process's memory
// size is far below 2^63, so neither branch can overflow.
func soakGrowth(first, last uint64) int64 {
	if last >= first {
		return int64(last - first) // #nosec G115 -- bounded by a process's memory size
	}
	return -int64(first - last) // #nosec G115 -- bounded by a process's memory size
}

func medianUint32(v []uint32) uint32 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	return s[(len(s)-1)/2]
}

func medianUint64(v []uint64) uint64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	return s[(len(s)-1)/2]
}

// Err reports the thresholds this run violated, wrapping ErrSoakThreshold,
// or nil when it met all of them.
func (r SoakReport) Err() error {
	if len(r.Violations) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrSoakThreshold, strings.Join(r.Violations, "; "))
}

// EnforcedErr is Err gated on cfg.Enforce: the violation when a soak asked
// for the thresholds to be applied, and nil otherwise. It exists so the one
// follow test can serve both callers without a mode flag of its own. The
// nightly job therefore records and reports every sample and verdict while
// keeping exactly the pass/fail conditions it had before the soak existed,
// and only an explicit SEP47IDX_SOAK_ENFORCE turns a verdict into a failure.
func (r SoakReport) EnforcedErr(cfg SoakConfig) error {
	if !cfg.Enforce {
		return nil
	}
	return r.Err()
}

// Markdown renders the report for a job summary or an artifact. It states
// the segmentation limit explicitly, because a chain of segments cannot
// prove that one process is leak-free for the whole span.
func (r SoakReport) Markdown() string {
	var b strings.Builder
	b.WriteString("# Testnet soak report\n\n")
	if r.Samples == 0 {
		b.WriteString("No samples were recorded.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "- Samples: %d (%d after catch-up) in %d segment(s)\n", r.Samples, r.CaughtUpSamples, len(r.Segments))
	fmt.Fprintf(&b, "- Wall-clock span: %s (%s to %s)\n", r.Span.Round(time.Second), r.Start.Format(time.RFC3339), r.End.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Lag after catch-up: median %d, max %d ledgers (limit %d)\n", r.MedianLag, r.MaxLag, r.Config.MaxLag)
	fmt.Fprintf(&b, "- Memory growth limits per segment: %d bytes, or x%.2f once growth passes %d bytes\n",
		r.Config.MaxGrowthBytes, r.Config.MaxGrowthRatio, soakRatioFloor(r.Config))
	fmt.Fprintf(&b, "- Segment duration: %s; sample interval: %s\n\n", r.Config.Duration, r.Config.SampleInterval)
	b.WriteString("| segment | samples | span | median lag | max lag | lag over limit | memory | first | last | growth | ratio | verdict |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, s := range r.Segments {
		fmt.Fprintf(&b, "| %d | %d | %s | %d | %d | %d | %s | %d | %d | %+d | %.2f | %s |\n",
			s.Segment, s.Samples, s.Span.Round(time.Second), s.MedianLag, s.MaxLag, s.LagViolations,
			s.MemorySource, s.FirstMemoryBytes, s.LastMemoryBytes, s.GrowthBytes, s.GrowthRatio, s.GrowthVerdict)
	}
	b.WriteString("\n")
	if len(r.Violations) == 0 {
		b.WriteString("Result: every configured threshold was met.\n")
	} else {
		b.WriteString("Result: thresholds violated.\n\n")
		for _, v := range r.Violations {
			fmt.Fprintf(&b, "- %s\n", v)
		}
	}
	b.WriteString("\nLag covers the whole chain. Memory growth is judged inside each segment only: " +
		"a segment is one process, so the resident-set series restarts at every boundary. " +
		"Segments show that each process is stable for its own span; they cannot show that a " +
		"single process is stable for the whole chain.\n")
	return b.String()
}

// WriteSoakReport writes the Markdown report to path, creating parent
// directories. An empty path writes nothing and returns nil.
func WriteSoakReport(path string, r SoakReport) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create report dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Clean(path), []byte(r.Markdown()), 0o600); err != nil {
		return fmt.Errorf("write report %s: %w", path, err)
	}
	return nil
}
