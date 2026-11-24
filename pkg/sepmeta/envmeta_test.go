package sepmeta

import (
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func envStream(t *testing.T, protocol, preRelease uint32) []byte {
	t.Helper()
	e := xdr.ScEnvMetaEntry{
		Kind:             xdr.ScEnvMetaKindScEnvMetaKindInterfaceVersion,
		InterfaceVersion: &xdr.ScEnvMetaEntryInterfaceVersion{Protocol: xdr.Uint32(protocol), PreRelease: xdr.Uint32(preRelease)},
	}
	b, err := e.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeEnvMeta(t *testing.T) {
	got, err := DecodeEnvMeta(envStream(t, 28, 3), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != 28 || got.PreRelease != 3 {
		t.Fatalf("got %+v, want protocol 28 pre-release 3", got)
	}
}

func TestDecodeEnvMetaRejectsBadInput(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, err := DecodeEnvMeta(nil, Limits{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		b := envStream(t, 21, 0)
		if _, err := DecodeEnvMeta(b[:len(b)-1], Limits{}); !errors.Is(err, ErrTruncated) {
			t.Fatalf("err = %v, want ErrTruncated", err)
		}
	})
	t.Run("unknown kind", func(t *testing.T) {
		// Kind 99 has no arm; the decoder must report it, not panic.
		if _, err := DecodeEnvMeta([]byte{0, 0, 0, 99}, Limits{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})
}

// TestDecodeEnvMetaFixture reads the value the Soroban toolchain wrote into a
// committed fixture, so a rebuild that changes the encoding is noticed.
func TestDecodeEnvMetaFixture(t *testing.T) {
	code := fixture(t, "token_full_sep.wasm")
	secs, err := ReadCustomSections(code, []string{SectionEnvMeta}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	section, ok := secs[SectionEnvMeta]
	if !ok {
		t.Fatal("token_full_sep.wasm has no contractenvmetav0 section")
	}
	got, err := DecodeEnvMeta(section, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != 28 || got.PreRelease != 0 {
		t.Fatalf("got %+v, want protocol 28 pre-release 0 (stellar-cli 28.0.0)", got)
	}
}
