package treg

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// MicroUSD is an amount of money in millionths of a US dollar: 1 USD is
// 1,000,000. Treg reports every amount it charges, reserves or holds as an
// integer number of micro-USD (the *_micro fields and the X-Treg-*-Micro
// headers); the SDK never converts one to a float.
//
// It decodes from a JSON integer only, and encodes as one.
type MicroUSD int64

// microPerUSD is the number of micro-USD in one dollar.
const microPerUSD = 1_000_000

// maxInt64Digits is the number of decimal digits of math.MaxInt64.
const maxInt64Digits = 19

// errDecimalUSD is the error ParseDecimalUSD returns. It never quotes the
// input.
var errDecimalUSD = errors.New("treg: invalid decimal USD amount")

// DecimalUSD returns m as an exact decimal number of dollars, with trailing
// zeros after the point removed: 1500000 is "1.5", 20000 is "0.02", 0 is
// "0" and -1 is "-0.000001". It is exact for every value, math.MinInt64
// included.
func (m MicroUSD) DecimalUSD() string {
	neg := m < 0
	mag := uint64(m) //nolint:gosec // the bit pattern is the point: negated below for negatives
	if neg {
		mag = -mag // two's complement: exact even for math.MinInt64
	}
	whole := strconv.FormatUint(mag/microPerUSD, 10)
	frac := mag % microPerUSD
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(whole)
	if frac != 0 {
		digits := strconv.FormatUint(frac+microPerUSD, 10)[1:] // six digits, zero-padded
		b.WriteByte('.')
		b.WriteString(strings.TrimRight(digits, "0"))
	}
	return b.String()
}

// String returns DecimalUSD prefixed with "$", such as "$0.02".
func (m MicroUSD) String() string {
	s := m.DecimalUSD()
	if strings.HasPrefix(s, "-") {
		return "-$" + s[1:]
	}
	return "$" + s
}

// ParseDecimalUSD parses a decimal number of dollars into micro-USD: an
// optional "-", one or more digits, and optionally a "." followed by one to
// six digits, such as "0.02", "12" or "-0.000001". There is no "+", no
// exponent, no thousands separator and no surrounding space; "-0" is 0. A
// value outside the int64 range is an error. The error never quotes s.
func ParseDecimalUSD(s string) (MicroUSD, error) {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	whole, frac, hasPoint := strings.Cut(s, ".")
	if whole == "" || !asciiDigits(whole) || (hasPoint && (frac == "" || len(frac) > 6 || !asciiDigits(frac))) {
		return 0, errDecimalUSD
	}
	w, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return 0, errDecimalUSD
	}
	var f uint64
	if frac != "" {
		f, _ = strconv.ParseUint(frac+strings.Repeat("0", 6-len(frac)), 10, 64) // at most six digits: never fails
	}
	if w > (1<<64-1-f)/microPerUSD {
		return 0, errDecimalUSD
	}
	mag := w*microPerUSD + f
	switch {
	case neg && mag <= 1<<63:
		return MicroUSD(-mag), nil //nolint:gosec // mag <= 2^63: -mag is exact in two's complement
	case !neg && mag < 1<<63:
		return MicroUSD(mag), nil //nolint:gosec // mag < 2^63 here
	}
	return 0, errDecimalUSD
}

// UnmarshalJSON accepts a JSON integer only: no quotes, fraction or
// exponent.
func (m *MicroUSD) UnmarshalJSON(b []byte) error {
	if m == nil {
		return errNilTarget
	}
	v, ok := parseJSONInt(b)
	if !ok {
		return errNotInteger
	}
	*m = MicroUSD(v)
	return nil
}

// Decimal is a JSON number kept as the exact text Treg sent, such as
// "0.006667" for a catalog price or "12.5" for a display amount in USD. The
// SDK offers no conversion to a float: amounts that matter are MicroUSD.
//
// It decodes from a JSON number only (a quoted string is refused) and
// encodes as the same number.
type Decimal string

// UnmarshalJSON keeps a JSON number as text and refuses anything else.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	if d == nil {
		return errNilTarget
	}
	b = bytes.TrimSpace(b)
	if !isJSONNumber(b) {
		return errNotNumber
	}
	*d = Decimal(b)
	return nil
}

// MarshalJSON encodes d as a JSON number; an empty or invalid Decimal
// encodes as null.
func (d Decimal) MarshalJSON() ([]byte, error) {
	if !isJSONNumber([]byte(d)) {
		return []byte("null"), nil
	}
	return []byte(d), nil
}

// Model decoding errors. They describe the shape, never the value.
var (
	errNilTarget  = errors.New("treg: decode into a nil pointer")
	errNotInteger = errors.New("treg: value is not a JSON integer")
	errNotNumber  = errors.New("treg: value is not a JSON number")
)

// parseJSONInt parses b, a JSON value, as an integer that fits an int64:
// an optional "-" and digits, with no leading zeros, fraction or exponent.
func parseJSONInt(b []byte) (int64, bool) {
	b = bytes.TrimSpace(b)
	digits := b
	if len(digits) > 0 && digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 || len(digits) > maxInt64Digits || !asciiDigits(string(digits)) || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	v, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// isJSONNumber reports whether b is exactly one JSON number.
func isJSONNumber(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	return json.Valid(b) && (b[0] == '-' || (b[0] >= '0' && b[0] <= '9'))
}
