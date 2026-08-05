package onchain

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var (
	ErrInvalidAmount   = errors.New("invalid decimal amount")
	ErrNegativeAmount  = errors.New("amount must not be negative")
	ErrExcessPrecision = errors.New("amount exceeds token precision")
)

// ParseDecimal converts a non-negative decimal string to token raw units.
// It never rounds and rejects scientific notation, signs, and excess scale.
func ParseDecimal(value string, decimals uint8) (*big.Int, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") {
		return nil, ErrInvalidAmount
	}
	if strings.HasPrefix(value, "-") {
		return nil, ErrNegativeAmount
	}
	if strings.Count(value, ".") > 1 {
		return nil, ErrInvalidAmount
	}

	parts := strings.SplitN(value, ".", 2)
	whole := parts[0]
	if whole == "" || !decimalDigits(whole) {
		return nil, ErrInvalidAmount
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || !decimalDigits(fraction) {
			return nil, ErrInvalidAmount
		}
	}
	if len(fraction) > int(decimals) {
		return nil, ErrExcessPrecision
	}

	rawDigits := strings.TrimLeft(whole+fraction+strings.Repeat("0", int(decimals)-len(fraction)), "0")
	if rawDigits == "" {
		rawDigits = "0"
	}
	raw, ok := new(big.Int).SetString(rawDigits, 10)
	if !ok {
		return nil, ErrInvalidAmount
	}
	return raw, nil
}

// FormatRaw returns the canonical fixed-scale decimal representation. Trailing
// fractional zeroes are removed; at least one whole digit is always emitted.
func FormatRaw(raw *big.Int, decimals uint8) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("raw amount is nil")
	}
	if raw.Sign() < 0 {
		return "", ErrNegativeAmount
	}
	digits := raw.String()
	if decimals == 0 {
		return digits, nil
	}
	if len(digits) <= int(decimals) {
		digits = strings.Repeat("0", int(decimals)-len(digits)+1) + digits
	}
	cut := len(digits) - int(decimals)
	whole := digits[:cut]
	fraction := strings.TrimRight(digits[cut:], "0")
	if fraction == "" {
		return whole, nil
	}
	return whole + "." + fraction, nil
}

func CompareRaw(left, right *big.Int) (int, error) {
	if left == nil || right == nil {
		return 0, fmt.Errorf("raw amounts must not be nil")
	}
	if left.Sign() < 0 || right.Sign() < 0 {
		return 0, ErrNegativeAmount
	}
	return left.Cmp(right), nil
}

func decimalDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
