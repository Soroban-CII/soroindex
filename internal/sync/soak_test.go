package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func envLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestParseSoakConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
		check   func(*testing.T, SoakConfig)
	}{
		{
			name: "no variables keeps the nightly 30-minute defaults",
			env:  nil,
			check: func(t *testing.T, c SoakConfig) {
				if c.Duration != 30*time.Minute {
					t.Errorf("duration = %s, want 30m", c.Duration)
				}
				if c.SampleInterval != time.Minute || c.MaxLag != DefaultSoakMaxLag {
					t.Errorf("interval = %s, max lag = %d", c.SampleInterval, c.MaxLag)
				}
				if c.MaxGrowthBytes != DefaultSoakMaxGrowthBytes || c.MaxGrowthRatio != DefaultSoakMaxGrowthRatio {
					t.Errorf("growth limits = %d, %v", c.MaxGrowthBytes, c.MaxGrowthRatio)
				}
				if c.Segment != 1 || c.SampleDir != "" || c.DBPath != "" || c.ReportPath != "" {
					t.Errorf("segment/paths = %d %q %q %q", c.Segment, c.SampleDir, c.DBPath, c.ReportPath)
				}
				if c.Enforce {
					t.Error("Enforce is on with no variable set; the nightly job would gain failure conditions")
				}
			},
		},
		{
			name: "an empty variable is treated as unset",
			env:  map[string]string{EnvSoakDuration: "", EnvSoakMaxLag: ""},
			check: func(t *testing.T, c SoakConfig) {
				if c.Duration != DefaultSoakDuration || c.MaxLag != DefaultSoakMaxLag {
					t.Errorf("duration = %s, max lag = %d", c.Duration, c.MaxLag)
				}
			},
		},
		{
			name: "a soak segment overrides every setting",
			env: map[string]string{
				EnvSoakDuration:       "4h48m",
				EnvSoakSampleInterval: "30s",
				EnvSoakMaxLag:         "40",
				EnvSoakMaxGrowthBytes: "1048576",
				EnvSoakMaxGrowthRatio: "1.25",
				EnvSoakSegment:        "3",
				EnvSoakSampleDir:      "/tmp/soak",
				EnvSoakDB:             "/tmp/soak/testnet.db",
				EnvSoakReport:         "/tmp/soak/report-3.md",
			},
			check: func(t *testing.T, c SoakConfig) {
				if c.Duration != 4*time.Hour+48*time.Minute {
					t.Errorf("duration = %s", c.Duration)
				}
				if c.SampleInterval != 30*time.Second || c.MaxLag != 40 {
					t.Errorf("interval = %s, max lag = %d", c.SampleInterval, c.MaxLag)
				}
				if c.MaxGrowthBytes != 1<<20 || c.MaxGrowthRatio != 1.25 {
					t.Errorf("growth limits = %d, %v", c.MaxGrowthBytes, c.MaxGrowthRatio)
				}
				if c.Segment != 3 || c.SampleDir != "/tmp/soak" || c.DBPath != "/tmp/soak/testnet.db" || c.ReportPath != "/tmp/soak/report-3.md" {
					t.Errorf("segment/paths = %d %q %q %q", c.Segment, c.SampleDir, c.DBPath, c.ReportPath)
				}
			},
		},
		{
			name:    "an unparsable duration is an error naming the variable",
			env:     map[string]string{EnvSoakDuration: "24"},
			wantErr: EnvSoakDuration + `="24"`,
		},
		{
			name:    "a zero duration is rejected",
			env:     map[string]string{EnvSoakDuration: "0s"},
			wantErr: "must be positive",
		},
		{
			name:    "a negative duration is rejected",
			env:     map[string]string{EnvSoakDuration: "-1h"},
			wantErr: "must be positive",
		},
		{
			name:    "a sample interval longer than the duration is rejected",
			env:     map[string]string{EnvSoakDuration: "10m", EnvSoakSampleInterval: "20m"},
			wantErr: "must not exceed",
		},
		{
			name:    "a non-numeric lag limit is an error",
			env:     map[string]string{EnvSoakMaxLag: "none"},
			wantErr: EnvSoakMaxLag,
		},
		{
			name:    "a growth ratio below 1 is rejected",
			env:     map[string]string{EnvSoakMaxGrowthRatio: "0.9"},
			wantErr: "ratio must be at least 1",
		},
		{
			name:    "segment 0 is rejected",
			env:     map[string]string{EnvSoakSegment: "0"},
			wantErr: "numbered from 1",
		},
		{
			name: "a long duration alone does not enable enforcement",
			env:  map[string]string{EnvSoakDuration: "4h48m", EnvSoakSegment: "3"},
			check: func(t *testing.T, c SoakConfig) {
				if c.Enforce {
					t.Error("Enforce is on from the duration alone; configuring a long run must not change pass/fail by itself")
				}
			},
		},
		{
			name: "a soak run turns enforcement on",
			env:  map[string]string{EnvSoakEnforce: "true"},
			check: func(t *testing.T, c SoakConfig) {
				if !c.Enforce {
					t.Errorf("Enforce is off with %s=true", EnvSoakEnforce)
				}
			},
		},
		{
			name: "enforcement accepts the usual boolean spellings",
			env:  map[string]string{EnvSoakEnforce: "1"},
			check: func(t *testing.T, c SoakConfig) {
				if !c.Enforce {
					t.Errorf("Enforce is off with %s=1", EnvSoakEnforce)
				}
			},
		},
		{
			name: "an explicit false keeps enforcement off",
			env:  map[string]string{EnvSoakEnforce: "false"},
			check: func(t *testing.T, c SoakConfig) {
				if c.Enforce {
					t.Errorf("Enforce is on with %s=false", EnvSoakEnforce)
				}
			},
		},
		{
			name:    "a non-boolean enforcement value is refused",
			env:     map[string]string{EnvSoakEnforce: "yes please"},
			wantErr: EnvSoakEnforce,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseSoakConfig(envLookup(tc.env))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}

// fakeClock returns times at a fixed step so interval gating is deterministic.
type fakeClock struct {
	now  time.Time
	step time.Duration
}

func (c *fakeClock) tick() time.Time {
	c.now = c.now.Add(c.step)
	return c.now
}

func TestSamplerRecordsOnePerIntervalAndWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	cfg := SoakConfig{Duration: 10 * time.Minute, SampleInterval: time.Minute, MaxLag: 25, Segment: 2, SampleDir: dir}
	s, err := NewSoakSampler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 20-second steps: every third Observe is due.
	clock := &fakeClock{now: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), step: 20 * time.Second}
	s.Now = clock.tick
	rss := uint64(100 << 20)
	s.Mem = func() SoakMemory {
		rss += 1 << 20
		return SoakMemory{RSSBytes: rss, HeapBytes: 1 << 20}
	}
	recorded := 0
	for i := range 9 {
		if _, ok := s.Observe(uint32(100+i), 102, i > 0); ok {
			recorded++
		}
	}
	if recorded != 3 {
		t.Fatalf("recorded %d samples from 9 observations at 20s steps, want 3", recorded)
	}
	if got := len(s.Samples()); got != 3 {
		t.Fatalf("in-memory samples = %d, want 3", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := SoakSampleFile(dir, 2)
	b, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("file has %d lines, want 3:\n%s", len(lines), b)
	}
	t.Logf("sample log %s:\n%s", path, b)
	loaded, err := LoadSoakSamples(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("loaded %d samples, want 3", len(loaded))
	}
	first := loaded[0]
	if first.Segment != 2 || first.LastLedger != 100 || first.LatestLedger != 102 || first.Lag != 2 {
		t.Errorf("first sample = %+v", first)
	}
	if first.CaughtUp {
		t.Errorf("first sample should not be marked caught up: %+v", first)
	}
	if first.MemorySource != "rss" || first.MemoryBytes != first.RSSBytes {
		t.Errorf("memory source = %q, bytes = %d, rss = %d", first.MemorySource, first.MemoryBytes, first.RSSBytes)
	}
	if loaded[2].ElapsedSeconds <= loaded[0].ElapsedSeconds {
		t.Errorf("elapsed did not advance: %v then %v", loaded[0].ElapsedSeconds, loaded[2].ElapsedSeconds)
	}
}

func TestSamplerWithoutProcfsFallsBackToHeap(t *testing.T) {
	s, err := NewSoakSampler(SoakConfig{Duration: time.Minute, SampleInterval: time.Second, Segment: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.Mem = func() SoakMemory { return SoakMemory{RSSBytes: 0, HeapBytes: 7 << 20} }
	got := s.Record(10, 10, true)
	if got.MemorySource != "heap" || got.MemoryBytes != 7<<20 {
		t.Fatalf("sample = %+v, want heap fallback", got)
	}
	if got.Lag != 0 {
		t.Fatalf("lag = %d, want 0 when last equals latest", got.Lag)
	}
}

func TestSamplerCaughtUpStickyWithoutRecording(t *testing.T) {
	s, err := NewSoakSampler(SoakConfig{Duration: time.Hour, SampleInterval: time.Hour, Segment: 1})
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), step: time.Second}
	s.Now = clock.tick
	s.Mem = func() SoakMemory { return SoakMemory{RSSBytes: 1 << 20} }
	if _, ok := s.Observe(5, 9, false); !ok {
		t.Fatal("the first observation should always record")
	}
	if _, ok := s.Observe(9, 9, true); ok {
		t.Fatal("an observation inside the interval should not record")
	}
	final := s.Record(9, 9, false)
	if !final.CaughtUp {
		t.Fatalf("caught-up should stay set once reported: %+v", final)
	}
}

func TestReadSoakMemoryReportsSomething(t *testing.T) {
	m := ReadSoakMemory()
	if m.HeapBytes == 0 {
		t.Fatal("heap in use is 0")
	}
	t.Logf("rss=%d heap=%d", m.RSSBytes, m.HeapBytes)
}

func TestSoakActionUsesWorkspaceAbsoluteArtifactPaths(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "actions", "soak-segment", "action.yml")
	b, err := os.ReadFile(path) // #nosec G304 -- fixed repository test fixture
	if err != nil {
		t.Fatal(err)
	}
	action := string(b)
	for _, want := range []string{
		`workspace="${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is required}"`,
		`SEP47IDX_SOAK_SAMPLE_DIR: ${{ steps.paths.outputs.sample_dir }}`,
		`SEP47IDX_SOAK_DB: ${{ steps.paths.outputs.db }}`,
		`SEP47IDX_SOAK_REPORT: ${{ steps.paths.outputs.report }}`,
		`for artifact in "$SOAK_DB" "$SOAK_SAMPLES" "$SOAK_REPORT" "$SOAK_LOG"`,
	} {
		if !strings.Contains(action, want) {
			t.Errorf("soak action does not contain %q", want)
		}
	}
	for _, relative := range []string{
		"SEP47IDX_SOAK_SAMPLE_DIR: soak/",
		"SEP47IDX_SOAK_DB: soak/",
		"SEP47IDX_SOAK_REPORT: soak/",
	} {
		if strings.Contains(action, relative) {
			t.Errorf("soak action still contains package-relative path %q", relative)
		}
	}
	if got := strings.Count(action, "if-no-files-found: error"); got != 2 {
		t.Errorf("mandatory artifact uploads using if-no-files-found: error = %d, want 2", got)
	}
}

func TestCheckSoakDatabaseRequiresCheckpointContinuity(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	state, err := CheckSoakDatabase(ctx, s)
	if err != nil || state.LastLedger != 0 {
		t.Fatalf("new database state = %+v, %v", state, err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := store.Checkpoint{Ledger: 42, Hash: strings.Repeat("ab", 32)}
	if err := tx.SetLastLedger(ctx, checkpoint.Ledger); err != nil {
		t.Fatal(err)
	}
	if err := tx.SetCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err = CheckSoakDatabase(ctx, s)
	if err != nil || state.LastLedger != checkpoint.Ledger || state.Checkpoint != checkpoint {
		t.Fatalf("resumable database state = %+v, %v; want %+v", state, err, checkpoint)
	}
	if err := s.SetState(ctx, store.KeyLastLedger, "43"); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSoakDatabase(ctx, s); err == nil || !strings.Contains(err.Error(), "differs from last_ledger 43") {
		t.Fatalf("mismatched checkpoint error = %v", err)
	}
}

// soakSeries builds samples for one segment with the given memory values.
func soakSeries(segment int, start time.Time, lags []uint32, mem []uint64, caughtUp bool) []SoakSample {
	out := make([]SoakSample, 0, len(mem))
	for i := range mem {
		lag := uint32(0)
		if i < len(lags) {
			lag = lags[i]
		}
		out = append(out, SoakSample{
			At:           start.Add(time.Duration(i) * time.Minute),
			Segment:      segment,
			LastLedger:   uint32(1000 + i),
			LatestLedger: uint32(1000+i) + lag,
			Lag:          lag,
			CaughtUp:     caughtUp,
			MemoryBytes:  mem[i],
			MemorySource: "rss",
			RSSBytes:     mem[i],
		})
	}
	return out
}

func flat(n int, v uint64) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func ramp(n int, from, step uint64) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = from + uint64(i)*step
	}
	return out
}

func zeros(n int) []uint32 { return make([]uint32, n) }

func TestEvaluateSoak(t *testing.T) {
	start := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	cfg := SoakConfig{Duration: time.Hour, SampleInterval: time.Minute, MaxLag: 25, MaxGrowthBytes: 64 << 20, MaxGrowthRatio: 2}
	tests := []struct {
		name       string
		samples    []SoakSample
		cfg        SoakConfig
		violations []string // substrings, in order
		check      func(*testing.T, SoakReport)
	}{
		{
			name:       "no samples is a violation, not a pass",
			samples:    nil,
			cfg:        cfg,
			violations: []string{"no samples"},
		},
		{
			name:    "flat memory and low lag passes",
			samples: soakSeries(1, start, zeros(12), flat(12, 200<<20), true),
			cfg:     cfg,
			check: func(t *testing.T, r SoakReport) {
				if r.Segments[0].GrowthVerdict != "ok" {
					t.Errorf("verdict = %q", r.Segments[0].GrowthVerdict)
				}
				if r.MaxLag != 0 || r.MedianLag != 0 {
					t.Errorf("lag median %d max %d", r.MedianLag, r.MaxLag)
				}
			},
		},
		{
			name:       "lag above the limit after catch-up fails",
			samples:    soakSeries(1, start, []uint32{0, 0, 90, 0, 0, 0, 0, 0}, flat(8, 100<<20), true),
			cfg:        cfg,
			violations: []string{"lag exceeded 25 ledgers in 1 of 8 samples"},
			check: func(t *testing.T, r SoakReport) {
				if r.MaxLag != 90 || r.WorstLag.Lag != 90 {
					t.Errorf("worst lag = %+v", r.WorstLag)
				}
			},
		},
		{
			name:    "catch-up backlog before caught_up is not a lag violation",
			samples: append(soakSeries(1, start, []uint32{5000, 4000}, flat(2, 100<<20), false), soakSeries(1, start.Add(10*time.Minute), zeros(8), flat(8, 100<<20), true)...),
			cfg:     cfg,
			check: func(t *testing.T, r SoakReport) {
				if r.LagViolations != 0 {
					t.Errorf("lag violations = %d", r.LagViolations)
				}
				if r.CaughtUpSamples != 8 || r.Samples != 10 {
					t.Errorf("samples = %d, caught up = %d", r.Samples, r.CaughtUpSamples)
				}
			},
		},
		{
			name:       "a never-caught-up run fails",
			samples:    soakSeries(1, start, []uint32{100, 100, 100, 100, 100, 100}, flat(6, 100<<20), false),
			cfg:        cfg,
			violations: []string{"never caught up"},
		},
		{
			name:       "memory growth above the byte limit fails",
			samples:    soakSeries(1, start, zeros(12), ramp(12, 100<<20, 16<<20), true),
			cfg:        cfg,
			violations: []string{"segment 1 memory (rss) grew", "above the limit of 67108864 bytes"},
			check: func(t *testing.T, r SoakReport) {
				if r.Segments[0].GrowthBytes <= 64<<20 {
					t.Errorf("growth = %d", r.Segments[0].GrowthBytes)
				}
			},
		},
		{
			// Growth of 40 MiB is under the 64 MiB byte budget but over the
			// 16 MiB ratio floor, so the ratio alone condemns the segment.
			name: "memory growth above the ratio limit fails even under the byte limit",
			samples: soakSeries(1, start, zeros(12),
				append(append(flat(4, 20<<20), flat(4, 40<<20)...), flat(4, 60<<20)...), true),
			cfg:        cfg,
			violations: []string{"x3.00", "or x2.00"},
		},
		{
			// The same ratio on a trivial base is warm-up, not a leak: 8 MiB
			// of growth never reaches the ratio floor.
			name:    "a large ratio below the floor is not a violation",
			samples: soakSeries(1, start, zeros(12), ramp(12, 1<<20, 1<<20), true),
			cfg:     cfg,
			check: func(t *testing.T, r SoakReport) {
				if r.Segments[0].GrowthVerdict != "ok" {
					t.Errorf("verdict = %q, growth %d bytes, ratio %.2f",
						r.Segments[0].GrowthVerdict, r.Segments[0].GrowthBytes, r.Segments[0].GrowthRatio)
				}
			},
		},
		{
			name:    "memory that falls back to its baseline passes",
			samples: soakSeries(1, start, zeros(12), append(append(flat(4, 100<<20), ramp(4, 400<<20, 0)...), flat(4, 100<<20)...), true),
			cfg:     cfg,
			check: func(t *testing.T, r SoakReport) {
				if r.Segments[0].GrowthVerdict != "ok" || r.Segments[0].GrowthBytes != 0 {
					t.Errorf("verdict %q growth %d", r.Segments[0].GrowthVerdict, r.Segments[0].GrowthBytes)
				}
			},
		},
		{
			name:    "a segment with too few samples is inconclusive, not a violation",
			samples: soakSeries(1, start, zeros(3), ramp(3, 1<<20, 900<<20), true),
			cfg:     cfg,
			check: func(t *testing.T, r SoakReport) {
				if r.Segments[0].GrowthVerdict != "inconclusive" {
					t.Errorf("verdict = %q", r.Segments[0].GrowthVerdict)
				}
			},
		},
		{
			name: "growth is judged per segment, never across a restart",
			// Each segment is flat; the second starts far above the
			// first. Across segments that looks like a 4x rise.
			samples: append(soakSeries(1, start, zeros(8), flat(8, 100<<20), true),
				soakSeries(2, start.Add(5*time.Hour), zeros(8), flat(8, 400<<20), true)...),
			cfg: cfg,
			check: func(t *testing.T, r SoakReport) {
				if len(r.Segments) != 2 {
					t.Fatalf("segments = %d", len(r.Segments))
				}
				for _, s := range r.Segments {
					if s.GrowthVerdict != "ok" {
						t.Errorf("segment %d verdict = %q", s.Segment, s.GrowthVerdict)
					}
				}
				if r.Span < 5*time.Hour {
					t.Errorf("span = %s, want the whole chain", r.Span)
				}
			},
		},
		{
			name: "one bad segment in a chain fails the whole run",
			samples: append(soakSeries(1, start, zeros(8), flat(8, 100<<20), true),
				soakSeries(2, start.Add(5*time.Hour), zeros(12), ramp(12, 100<<20, 20<<20), true)...),
			cfg:        cfg,
			violations: []string{"segment 2 memory"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := EvaluateSoak(tc.samples, tc.cfg)
			if len(tc.violations) == 0 {
				if err := r.Err(); err != nil {
					t.Fatalf("want no violation, got %v", err)
				}
			} else {
				err := r.Err()
				if err == nil {
					t.Fatalf("want violations %v, got none\n%s", tc.violations, r.Markdown())
				}
				if !errors.Is(err, ErrSoakThreshold) {
					t.Errorf("error does not wrap ErrSoakThreshold: %v", err)
				}
				for _, want := range tc.violations {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

func TestSoakReportMarkdownStatesTheSegmentLimit(t *testing.T) {
	start := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	cfg := SoakConfig{Duration: 4*time.Hour + 48*time.Minute, SampleInterval: time.Minute, MaxLag: 25, MaxGrowthBytes: 64 << 20, MaxGrowthRatio: 2}
	samples := append(soakSeries(1, start, zeros(8), flat(8, 100<<20), true),
		soakSeries(2, start.Add(5*time.Hour), zeros(8), flat(8, 110<<20), true)...)
	md := EvaluateSoak(samples, cfg).Markdown()
	for _, want := range []string{"# Testnet soak report", "| segment |", "Result: every configured threshold was met.", "cannot show that a single process is stable for the whole chain"} {
		if !strings.Contains(md, want) {
			t.Errorf("report does not contain %q:\n%s", want, md)
		}
	}
	path := filepath.Join(t.TempDir(), "nested", "report-2.md")
	if err := WriteSoakReport(path, EvaluateSoak(samples, cfg)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != md {
		t.Error("written report differs from Markdown()")
	}
	if err := WriteSoakReport("", EvaluateSoak(samples, cfg)); err != nil {
		t.Fatalf("empty path should be a no-op: %v", err)
	}
	t.Logf("rendered report:\n%s", md)
}

func TestLoadSoakSamples(t *testing.T) {
	t.Run("a missing directory yields no samples and no error", func(t *testing.T) {
		got, err := LoadSoakSamples(filepath.Join(t.TempDir(), "absent"))
		if err != nil || len(got) != 0 {
			t.Fatalf("got %d samples, err %v", len(got), err)
		}
	})
	t.Run("segments load in order and other files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		written := []struct {
			segment int
			ledger  uint32
		}{{segment: 2, ledger: 20}, {segment: 1, ledger: 10}}
		for _, w := range written {
			s, err := NewSoakSampler(SoakConfig{SampleInterval: time.Second, Segment: w.segment, SampleDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			s.Mem = func() SoakMemory { return SoakMemory{RSSBytes: 1 << 20} }
			s.Record(w.ledger, w.ledger, true)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "report-1.md"), []byte("not samples"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadSoakSamples(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Segment != 1 || got[1].Segment != 2 {
			t.Fatalf("loaded %+v", got)
		}
	})
	t.Run("a corrupt line names the file and line", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(SoakSampleFile(dir, 1), []byte("{}\n\nnot json\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadSoakSamples(dir)
		if err == nil || !strings.Contains(err.Error(), "line 3") {
			t.Fatalf("err = %v, want it to name line 3", err)
		}
	})
}

// TestFollowLoopSamplingEndToEnd wires the sampler into a real follow loop,
// as integration_test.go does, with a fake source instead of testnet: a
// tiny duration, samples written to a file, the series reloaded from disk,
// and the thresholds evaluated. It is the network-free end-to-end check of
// the soak path.
func TestFollowLoopSamplingEndToEnd(t *testing.T) {
	run := func(t *testing.T, mem func() SoakMemory, cfg SoakConfig) SoakReport {
		t.Helper()
		s := testStore(t)
		sampler, err := NewSoakSampler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sampler.Mem = mem
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
		defer cancel()
		l := Loop{
			Store:       s,
			Source:      &source{oldest: 1, latest: 5},
			Apply:       writeFact,
			StartLedger: 1,
			Follow:      true,
			Interval:    time.Millisecond,
		}
		l.Progress = func(last, latest uint32, done bool) {
			sampler.Observe(last, latest, done)
		}
		if err := l.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("follow: %v", err)
		}
		last, err := s.State(context.Background(), store.KeyLastLedger)
		if err != nil {
			t.Fatal(err)
		}
		if last != "5" {
			t.Fatalf("last_ledger = %q, want 5", last)
		}
		sampler.Record(5, 5, true)
		if err := sampler.Close(); err != nil {
			t.Fatal(err)
		}
		series, err := LoadSoakSamples(cfg.SampleDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(series) < MinSoakGrowthSamples {
			t.Fatalf("recorded %d samples, want at least %d", len(series), MinSoakGrowthSamples)
		}
		report := EvaluateSoak(series, cfg)
		if err := WriteSoakReport(cfg.ReportPath, report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	base := func(dir string) SoakConfig {
		return SoakConfig{
			Duration:       300 * time.Millisecond,
			SampleInterval: time.Millisecond,
			MaxLag:         25,
			MaxGrowthBytes: 64 << 20,
			MaxGrowthRatio: 2,
			Segment:        1,
			SampleDir:      dir,
			ReportPath:     filepath.Join(dir, "report-001.md"),
		}
	}
	t.Run("a healthy follow loop records samples and meets every threshold", func(t *testing.T) {
		dir := t.TempDir()
		report := run(t, func() SoakMemory { return SoakMemory{RSSBytes: 120 << 20, HeapBytes: 4 << 20} }, base(dir))
		if err := report.Err(); err != nil {
			t.Fatalf("thresholds: %v\n%s", err, report.Markdown())
		}
		if report.CaughtUpSamples == 0 || report.MaxLag != 0 {
			t.Errorf("caught up samples = %d, max lag = %d", report.CaughtUpSamples, report.MaxLag)
		}
		b, err := os.ReadFile(filepath.Join(dir, "report-001.md")) // #nosec G304 -- test-controlled path
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("report file:\n%s", b)
		raw, err := os.ReadFile(SoakSampleFile(dir, 1)) // #nosec G304 -- test-controlled path
		if err != nil {
			t.Fatal(err)
		}
		head := strings.SplitN(string(raw), "\n", 4)
		t.Logf("sample log %s (first lines of %d samples):\n%s", SoakSampleFile(dir, 1), report.Samples, strings.Join(head[:min(3, len(head))], "\n"))
	})
	t.Run("a growing process fails the run", func(t *testing.T) {
		dir := t.TempDir()
		rss := uint64(100 << 20)
		report := run(t, func() SoakMemory {
			rss += 4 << 20
			return SoakMemory{RSSBytes: rss, HeapBytes: 4 << 20}
		}, base(dir))
		err := report.Err()
		if err == nil || !errors.Is(err, ErrSoakThreshold) {
			t.Fatalf("err = %v, want a threshold violation\n%s", err, report.Markdown())
		}
		t.Logf("violation: %v", err)
	})
	t.Run("a stalled index fails the lag threshold", func(t *testing.T) {
		dir := t.TempDir()
		cfg := base(dir)
		cfg.MaxLag = 1
		sampler, err := NewSoakSampler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sampler.Mem = func() SoakMemory { return SoakMemory{RSSBytes: 100 << 20} }
		// A tip that runs ahead of a frozen index is what a stuck writer
		// looks like to the sampler.
		for i := range 10 {
			sampler.Observe(100, uint32(100+i*10), true)
			time.Sleep(2 * time.Millisecond)
		}
		if err := sampler.Close(); err != nil {
			t.Fatal(err)
		}
		series, err := LoadSoakSamples(dir)
		if err != nil {
			t.Fatal(err)
		}
		report := EvaluateSoak(series, cfg)
		if err := report.Err(); err == nil || !strings.Contains(err.Error(), "lag exceeded 1 ledgers") {
			t.Fatalf("err = %v, want a lag violation", err)
		} else {
			t.Logf("violation: %v", err)
		}
	})
}

// TestEvaluateSoakRatioNeedsMeaningfulAbsoluteGrowth pins the growth rule
// against two failure modes. A cold process warming up doubles a small
// resident set in the first minutes of a segment, which is not a leak and
// must not fail the nightly 30-minute follow. A leak on a large baseline
// grows by a lot while barely moving the ratio, and must still fail.
func TestEvaluateSoakRatioNeedsMeaningfulAbsoluteGrowth(t *testing.T) {
	start := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	// The shipped defaults: a 256 MiB budget, so the ratio floor is 64 MiB.
	cfg := SoakConfig{
		Duration: time.Hour, SampleInterval: time.Minute, MaxLag: 25,
		MaxGrowthBytes: DefaultSoakMaxGrowthBytes, MaxGrowthRatio: DefaultSoakMaxGrowthRatio,
	}
	step := func(low, high uint64) []uint64 {
		out := make([]uint64, 0, 12)
		for i := 0; i < 4; i++ {
			out = append(out, low)
		}
		for i := 0; i < 4; i++ {
			out = append(out, (low+high)/2)
		}
		for i := 0; i < 4; i++ {
			out = append(out, high)
		}
		return out
	}
	tests := []struct {
		name        string
		low, high   uint64
		wantVerdict string
	}{
		{
			name: "warm-up doubling a small base is not a leak",
			low:  40 << 20, high: 90 << 20, // +50 MiB, x2.25
			wantVerdict: "ok",
		},
		{
			name: "proportional growth past the floor is a leak",
			low:  100 << 20, high: 300 << 20, // +200 MiB, x3.00
			wantVerdict: "violation",
		},
		{
			name: "growth past the budget is a leak whatever the ratio",
			low:  1024 << 20, high: 1536 << 20, // +512 MiB, x1.50
			wantVerdict: "violation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := EvaluateSoak(soakSeries(1, start, zeros(12), step(tt.low, tt.high), true), cfg)
			got := r.Segments[0].GrowthVerdict
			if got != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q (first=%d last=%d growth=%d ratio=%.2f)",
					got, tt.wantVerdict, r.Segments[0].FirstMemoryBytes, r.Segments[0].LastMemoryBytes,
					r.Segments[0].GrowthBytes, r.Segments[0].GrowthRatio)
			}
			if (r.Err() != nil) != (tt.wantVerdict == "violation") {
				t.Errorf("Err() = %v, want violation=%t", r.Err(), tt.wantVerdict == "violation")
			}
		})
	}
}

// TestEnforcedErrSeparatesNightlyFromSoak pins the one difference between
// the nightly 30-minute follow and a soak segment. Both record samples and
// both evaluate the same thresholds; only a soak fails on a violation. A
// regression here would either make the nightly job go red on a warm-up
// memory rise or make a real 24-hour leak pass silently.
func TestEnforcedErrSeparatesNightlyFromSoak(t *testing.T) {
	start := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	nightly := SoakConfig{
		Duration: 30 * time.Minute, SampleInterval: time.Minute, MaxLag: DefaultSoakMaxLag,
		MaxGrowthBytes: DefaultSoakMaxGrowthBytes, MaxGrowthRatio: DefaultSoakMaxGrowthRatio,
		Segment: 1,
	}
	soak := nightly
	soak.Enforce = true

	// A leak well past both the absolute budget and the ratio floor.
	leaking := make([]SoakSample, 0, 12)
	rss := uint64(200 << 20)
	for i := range 12 {
		rss += 64 << 20
		leaking = append(leaking, SoakSample{
			At: start.Add(time.Duration(i) * time.Minute), Segment: 1,
			LastLedger: uint32(1000 + i), LatestLedger: uint32(1000 + i), CaughtUp: true,
			MemoryBytes: rss, MemorySource: "rss", RSSBytes: rss,
		})
	}
	// A flat process that violates nothing.
	healthy := make([]SoakSample, 0, 12)
	for i := range 12 {
		healthy = append(healthy, SoakSample{
			At: start.Add(time.Duration(i) * time.Minute), Segment: 1,
			LastLedger: uint32(1000 + i), LatestLedger: uint32(1000 + i), CaughtUp: true,
			MemoryBytes: 200 << 20, MemorySource: "rss", RSSBytes: 200 << 20,
		})
	}

	t.Run("a violation is reported but does not fail the nightly run", func(t *testing.T) {
		report := EvaluateSoak(leaking, nightly)
		if err := report.Err(); err == nil {
			t.Fatalf("the series should violate a threshold\n%s", report.Markdown())
		} else {
			t.Logf("reported: %v", err)
		}
		if err := report.EnforcedErr(nightly); err != nil {
			t.Errorf("EnforcedErr = %v, want nil without %s", err, EnvSoakEnforce)
		}
	})
	t.Run("the same violation fails a soak run", func(t *testing.T) {
		report := EvaluateSoak(leaking, soak)
		err := report.EnforcedErr(soak)
		if err == nil || !errors.Is(err, ErrSoakThreshold) {
			t.Fatalf("EnforcedErr = %v, want ErrSoakThreshold\n%s", err, report.Markdown())
		}
		if report.Err().Error() != err.Error() {
			t.Errorf("EnforcedErr %q differs from Err %q", err, report.Err())
		}
	})
	t.Run("a healthy series passes in both modes", func(t *testing.T) {
		for _, cfg := range []SoakConfig{nightly, soak} {
			report := EvaluateSoak(healthy, cfg)
			if err := report.Err(); err != nil {
				t.Fatalf("enforce=%t: Err = %v\n%s", cfg.Enforce, err, report.Markdown())
			}
			if err := report.EnforcedErr(cfg); err != nil {
				t.Errorf("enforce=%t: EnforcedErr = %v", cfg.Enforce, err)
			}
		}
	})
}
