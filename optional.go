package treg

import (
	"bytes"
	"encoding/json"
)

// optionalState is the state of an Optional: absent, null or a value.
type optionalState uint8

const (
	optionalAbsent optionalState = iota
	optionalNull
	optionalValue
)

// Optional holds a JSON member that may be absent, null or a value, keeping
// the three apart: an absent key, an explicit null, a zero value and false
// are all different. Every nullable or optional field of an SDK model is an
// Optional.
//
// encoding/json calls UnmarshalJSON with "null" for an explicit null and
// never for an absent key, so a decoded Optional is absent exactly when the
// key was missing. Marshaled with the `json:",omitzero"` option, an absent
// Optional is omitted and a null one encodes as null.
type Optional[T any] struct {
	value T
	state optionalState
}

// Some returns an Optional holding v.
func Some[T any](v T) Optional[T] { return Optional[T]{value: v, state: optionalValue} }

// Null returns an Optional holding an explicit null.
func Null[T any]() Optional[T] { return Optional[T]{state: optionalNull} }

// Get returns the value and true when the member held a value, and the zero
// value and false when it was absent or null.
func (o Optional[T]) Get() (T, bool) {
	return o.value, o.state == optionalValue
}

// IsNull reports whether the member was an explicit null.
func (o Optional[T]) IsNull() bool { return o.state == optionalNull }

// IsPresent reports whether the member was present: a value or null.
func (o Optional[T]) IsPresent() bool { return o.state != optionalAbsent }

// IsZero reports whether the member was absent, so that `json:",omitzero"`
// omits it.
func (o Optional[T]) IsZero() bool { return o.state == optionalAbsent }

// UnmarshalJSON records null, or decodes the value into T.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	if o == nil {
		return errNilTarget
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		var zero T
		o.value, o.state = zero, optionalNull
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.value, o.state = v, optionalValue
	return nil
}

// MarshalJSON encodes the value, or null when the member is null or absent.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if o.state != optionalValue {
		return []byte("null"), nil
	}
	return json.Marshal(o.value)
}
