package sepmeta

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultLimitsAreUnchanged(t *testing.T) {
	// CLAUDE.md §5.1 fixes these numbers. A deployment that sets no limit
	// flag must parse exactly what every earlier release parsed, so this
	// test fails if a default is ever moved by accident.
	got := DefaultLimits()
	want := Limits{MaxWasmBytes: 1 << 20, MaxSectionBytes: 256 << 10, MaxSections: 10_000, MaxXDRDepth: 500}
	if got != want {
		t.Fatalf("DefaultLimits() = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("the defaults must validate: %v", err)
	}
}

func TestLimitsValidate(t *testing.T) {
	d := DefaultLimits()
	with := func(f func(*Limits)) Limits {
		l := d
		f(&l)
		return l
	}
	tests := []struct {
		name      string
		lim       Limits
		wantField string // "" means the configuration is accepted
	}{
		{name: "the shipped defaults are accepted", lim: d},
		{
			name: "MaxWasmBytes at the hard cap is accepted",
			lim:  with(func(l *Limits) { l.MaxWasmBytes = MaxConfigurableWasmBytes }),
		},
		{
			name:      "MaxWasmBytes one byte over the hard cap is rejected",
			lim:       with(func(l *Limits) { l.MaxWasmBytes = MaxConfigurableWasmBytes + 1 }),
			wantField: "MaxWasmBytes",
		},
		{
			name:      "MaxWasmBytes of zero is rejected rather than defaulted",
			lim:       with(func(l *Limits) { l.MaxWasmBytes = 0 }),
			wantField: "MaxWasmBytes",
		},
		{
			name:      "a negative MaxWasmBytes is rejected",
			lim:       with(func(l *Limits) { l.MaxWasmBytes = -1 }),
			wantField: "MaxWasmBytes",
		},
		{
			name: "MaxSectionBytes equal to MaxWasmBytes is accepted",
			lim:  with(func(l *Limits) { l.MaxSectionBytes = l.MaxWasmBytes }),
		},
		{
			name:      "MaxSectionBytes larger than the module that holds it is rejected",
			lim:       with(func(l *Limits) { l.MaxSectionBytes = l.MaxWasmBytes + 1 }),
			wantField: "MaxSectionBytes",
		},
		{
			name:      "MaxSectionBytes of zero is rejected",
			lim:       with(func(l *Limits) { l.MaxSectionBytes = 0 }),
			wantField: "MaxSectionBytes",
		},
		{
			name: "MaxSections at two bytes per section is accepted",
			lim:  with(func(l *Limits) { l.MaxSections = l.MaxWasmBytes / 2 }),
		},
		{
			name:      "more sections than a module of MaxWasmBytes can hold is rejected",
			lim:       with(func(l *Limits) { l.MaxSections = l.MaxWasmBytes/2 + 1 }),
			wantField: "MaxSections",
		},
		{
			name:      "MaxSections of zero is rejected",
			lim:       with(func(l *Limits) { l.MaxSections = 0 }),
			wantField: "MaxSections",
		},
		{
			name: "MaxXDRDepth at the decoder's own maximum is accepted",
			lim:  with(func(l *Limits) { l.MaxXDRDepth = MaxConfigurableXDRDepth }),
		},
		{
			name:      "MaxXDRDepth one over the decoder's maximum is rejected",
			lim:       with(func(l *Limits) { l.MaxXDRDepth = MaxConfigurableXDRDepth + 1 }),
			wantField: "MaxXDRDepth",
		},
		{
			name:      "MaxXDRDepth of zero is rejected",
			lim:       with(func(l *Limits) { l.MaxXDRDepth = 0 }),
			wantField: "MaxXDRDepth",
		},
		{
			name: "a lowered but self-consistent configuration is accepted",
			lim:  Limits{MaxWasmBytes: 4096, MaxSectionBytes: 1024, MaxSections: 64, MaxXDRDepth: 50},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.lim.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidLimit) {
				t.Fatalf("Validate() = %v, want ErrInvalidLimit", err)
			}
			var re *LimitRangeError
			if !errors.As(err, &re) || re.Limit != tt.wantField {
				t.Fatalf("error = %+v, want field %s", re, tt.wantField)
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Errorf("message %q does not name the limit", err.Error())
			}
		})
	}
}

func TestRaisedLimitAcceptsALargerModule(t *testing.T) {
	// A module just over the default total size, carrying a readable meta
	// section plus a large section this package never reads.
	meta := custom("contractmetav0", []byte("meta"))
	bloat := custom("bloat", make([]byte, DefaultLimits().MaxWasmBytes))
	big := module(meta, bloat)
	if len(big) <= DefaultLimits().MaxWasmBytes {
		t.Fatalf("fixture is %d bytes, which the defaults would accept", len(big))
	}

	t.Run("the default limit rejects it, naming MaxWasmBytes", func(t *testing.T) {
		_, err := ReadCustomSections(big, []string{"contractmetav0"}, DefaultLimits())
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != "MaxWasmBytes" {
			t.Fatalf("err = %v, want a MaxWasmBytes LimitError", err)
		}
	})

	t.Run("a raised MaxWasmBytes reads the same module", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxWasmBytes = 4 << 20
		if err := lim.Validate(); err != nil {
			t.Fatalf("raised limits must validate: %v", err)
		}
		got, err := ReadCustomSections(big, []string{"contractmetav0"}, lim)
		if err != nil {
			t.Fatalf("ReadCustomSections = %v, want nil", err)
		}
		if string(got["contractmetav0"]) != "meta" {
			t.Fatalf("meta = %q, want \"meta\"", got["contractmetav0"])
		}
	})

	t.Run("raising the total size does not raise the per-section limit", func(t *testing.T) {
		// The large section is still refused when it is one the caller asks
		// for: a raised total must not loosen MaxSectionBytes.
		lim := DefaultLimits()
		lim.MaxWasmBytes = 4 << 20
		_, err := ReadCustomSections(big, []string{"bloat"}, lim)
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != "MaxSectionBytes" {
			t.Fatalf("err = %v, want a MaxSectionBytes LimitError", err)
		}
	})
}
