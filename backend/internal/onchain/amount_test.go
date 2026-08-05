package onchain

import (
	"errors"
	"math/big"
	"testing"
)

func TestAmountRoundTrip(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"0": "0", "10": "10", "0.000001": "0.000001",
		"100000.123456": "100000.123456", "1.230000": "1.23",
	}
	for input, expected := range tests {
		raw, err := ParseDecimal(input, USDTDecimals)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", input, err)
		}
		formatted, err := FormatRaw(raw, USDTDecimals)
		if err != nil {
			t.Fatalf("FormatRaw(%q): %v", input, err)
		}
		if formatted != expected {
			t.Fatalf("FormatRaw(ParseDecimal(%q)) = %q, want %q", input, formatted, expected)
		}
	}
}

func TestAmountRejectsInvalidAndExcessPrecision(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", ".1", "1.", "1e6", "+1", "abc"} {
		if _, err := ParseDecimal(input, USDTDecimals); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("ParseDecimal(%q) error = %v, want ErrInvalidAmount", input, err)
		}
	}
	if _, err := ParseDecimal("1.0000001", USDTDecimals); !errors.Is(err, ErrExcessPrecision) {
		t.Fatalf("error = %v, want ErrExcessPrecision", err)
	}
	if _, err := FormatRaw(big.NewInt(-1), USDTDecimals); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("error = %v, want ErrNegativeAmount", err)
	}
}

func TestAmountSupportsValuesBeyondUint64(t *testing.T) {
	t.Parallel()
	raw, err := ParseDecimal("18446744073709551616.000001", USDTDecimals)
	if err != nil {
		t.Fatal(err)
	}
	if raw.BitLen() <= 64 {
		t.Fatalf("expected value beyond uint64, got bit length %d", raw.BitLen())
	}
}
