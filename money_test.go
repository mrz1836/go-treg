package treg_test

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
)

func TestMicroUSDDecimalUSD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   treg.MicroUSD
		want string
	}{
		{0, "0"},
		{1, "0.000001"},
		{-1, "-0.000001"},
		{20_000, "0.02"},
		{1_500_000, "1.5"},
		{1_000_000, "1"},
		{123_456_789, "123.456789"},
		{-123_456_789, "-123.456789"},
		{math.MaxInt64, "9223372036854.775807"},
		{math.MinInt64, "-9223372036854.775808"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.in.DecimalUSD())
			back, err := treg.ParseDecimalUSD(tt.want)
			require.NoError(t, err)
			assert.Equal(t, tt.in, back)
		})
	}
}

func TestMicroUSDString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$0.02", treg.MicroUSD(20_000).String())
	assert.Equal(t, "-$1.5", treg.MicroUSD(-1_500_000).String())
	assert.Equal(t, "$0", treg.MicroUSD(0).String())
}

func TestParseDecimalUSD(t *testing.T) {
	t.Parallel()
	valid := map[string]treg.MicroUSD{
		"0": 0, "-0": 0, "007": 7_000_000, "0.1": 100_000, "0.000001": 1, "12.000000": 12_000_000,
		"-0.5": -500_000,
	}
	for in, want := range valid {
		t.Run("valid_"+in, func(t *testing.T) {
			t.Parallel()
			got, err := treg.ParseDecimalUSD(in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	invalid := []string{
		"", "-", ".", "1.", ".5", "+1", "1e3", "1.0000001", " 1", "1 ", "1,000", "0x10", "--1", "1.-1",
		"9223372036854.775808", "-9223372036854.775809", "99999999999999999999", "١",
	}
	for _, in := range invalid {
		t.Run("invalid_"+in, func(t *testing.T) {
			t.Parallel()
			_, err := treg.ParseDecimalUSD(in)
			require.Error(t, err)
			if in != "" {
				assert.NotContains(t, err.Error(), in)
			}
		})
	}
}

func TestMicroUSDJSON(t *testing.T) {
	t.Parallel()
	var m treg.MicroUSD
	require.NoError(t, json.Unmarshal([]byte(" 1500 "), &m))
	assert.Equal(t, treg.MicroUSD(1500), m)
	for _, bad := range []string{`"1500"`, `1.5`, `1e3`, `01`, `null`, `true`, `99999999999999999999`} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			require.Error(t, json.Unmarshal([]byte(bad), new(treg.MicroUSD)))
		})
	}
	out, err := json.Marshal(treg.MicroUSD(-7))
	require.NoError(t, err)
	assert.Equal(t, "-7", string(out))
}

func TestDecimalJSON(t *testing.T) {
	t.Parallel()
	var d treg.Decimal
	require.NoError(t, json.Unmarshal([]byte("0.006667"), &d))
	assert.Equal(t, treg.Decimal("0.006667"), d)
	require.NoError(t, json.Unmarshal([]byte("1E-7"), &d))
	assert.Equal(t, treg.Decimal("1E-7"), d)
	for _, bad := range []string{`"0.1"`, `null`, `true`, `{}`} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			require.Error(t, json.Unmarshal([]byte(bad), new(treg.Decimal)))
		})
	}
	out, err := json.Marshal(struct {
		A treg.Decimal `json:"a"`
		B treg.Decimal `json:"b"`
	}{A: "12.50", B: "not a number"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":12.50,"b":null}`, string(out))
	assert.Contains(t, string(out), `"a":12.50`, "the lexeme is kept")
}

func TestOptional(t *testing.T) {
	t.Parallel()
	type doc struct {
		A treg.Optional[int64]  `json:"a,omitzero"`
		B treg.Optional[string] `json:"b,omitzero"`
		C treg.Optional[bool]   `json:"c,omitzero"`
	}
	var d doc
	require.NoError(t, json.Unmarshal([]byte(`{"a":0,"b":null}`), &d))
	v, ok := d.A.Get()
	assert.True(t, ok)
	assert.Equal(t, int64(0), v)
	assert.True(t, d.A.IsPresent())
	assert.False(t, d.A.IsNull())
	assert.True(t, d.B.IsNull())
	assert.True(t, d.B.IsPresent())
	_, ok = d.B.Get()
	assert.False(t, ok)
	assert.False(t, d.C.IsPresent())
	assert.True(t, d.C.IsZero())

	out, err := json.Marshal(d)
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":0,"b":null}`, string(out), "absent stays absent, null stays null")

	assert.Equal(t, treg.Some(int64(5)), func() treg.Optional[int64] {
		var o treg.Optional[int64]
		require.NoError(t, json.Unmarshal([]byte("5"), &o))
		return o
	}())
	assert.True(t, treg.Null[string]().IsNull())
	require.Error(t, json.Unmarshal([]byte(`"x"`), new(treg.Optional[int64])))
}

// FuzzDecimalUSD checks ParseDecimalUSD against an oracle that shares none
// of its code: the documented grammar, as a regular expression, and the
// exact value of the input as a math/big rational. An input the grammar
// accepts is parsed exactly when its value times 1e6, always an integer, is
// within the int64 range, and then to that integer, which DecimalUSD prints
// back exactly. Every other input is refused with the same fixed error, so
// that the error can never quote it. Among the refused inputs, big.Rat reads
// many that the grammar refuses by design, such as "+1", "1e3", ".5", "1.",
// "1/2" and "0x10"; for the rest of the refusals, the in-grammar ones, the
// oracle's rational lies outside the int64 range of micro-USD.
func FuzzDecimalUSD(f *testing.F) {
	for _, s := range []string{
		"0", "-0", "0.02", "1.5", "-0.000001", "9223372036854.775807", "-9223372036854.775808", "1e3", "",
		"9223372036854.775808", "-9223372036854.775809", "18446744073709.551615", "18446744073709.551616",
		"99999999999999999999", "007", "+1", ".5", "1.", "1/2", "0x10", "1.0000001", "-", "1_000", "١",
	} {
		f.Add(s)
	}
	grammar := regexp.MustCompile(`^-?[0-9]+(\.[0-9]{1,6})?$`)
	_, refusal := treg.ParseDecimalUSD("")
	require.Error(f, refusal)
	f.Fuzz(func(t *testing.T, s string) {
		m, err := treg.ParseDecimalUSD(s)
		if !grammar.MatchString(s) {
			require.ErrorIs(t, err, refusal, "outside the grammar")
			assert.Equal(t, refusal.Error(), err.Error())
			return
		}
		exact, ok := new(big.Rat).SetString(s)
		require.True(t, ok, "big.Rat reads every input of the grammar")
		micro := new(big.Rat).Mul(exact, big.NewRat(1_000_000, 1))
		require.True(t, micro.IsInt(), "at most six decimals make whole micro-USD")
		if !micro.Num().IsInt64() {
			require.ErrorIs(t, err, refusal, "outside the int64 range")
			assert.Equal(t, refusal.Error(), err.Error())
			return
		}
		require.NoError(t, err)
		assert.Equal(t, micro.Num().Int64(), int64(m), "the exact value")
		printed, ok := new(big.Rat).SetString(m.DecimalUSD())
		require.True(t, ok)
		assert.Zero(t, exact.Cmp(printed), "DecimalUSD prints the exact value")
		back, err := treg.ParseDecimalUSD(m.DecimalUSD())
		require.NoError(t, err)
		assert.Equal(t, m, back, "round trip")
	})
}
