package testutil

import (
	"io"
	"sync/atomic"
)

// RepeatBody is a response body that streams one line over and over, without
// end, as a server that never stops sending well-formed lines does, and
// counts its closes. Unlike an EndlessBody, its content parses line by line.
// Bound it with io.LimitReader, joined to it by a ReadCloser, for a finite
// body. Read is for one goroutine; Close may come from any.
type RepeatBody struct {
	line   string
	off    int
	closes atomic.Int32
}

// NewRepeatBody returns a RepeatBody that repeats line, which should end
// with its newline. An empty line makes an empty body.
func NewRepeatBody(line string) *RepeatBody { return &RepeatBody{line: line} }

// Read fills p with the next bytes of the repeated line, or returns io.EOF
// when the line is empty.
func (b *RepeatBody) Read(p []byte) (int, error) {
	if b.line == "" {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		c := copy(p[n:], b.line[b.off:])
		n += c
		b.off = (b.off + c) % len(b.line)
	}
	return n, nil
}

// Close counts the close; reads go on, as the body has no end to reach.
func (b *RepeatBody) Close() error {
	b.closes.Add(1)
	return nil
}

// Closes returns the number of Close calls.
func (b *RepeatBody) Closes() int { return int(b.closes.Load()) }

// ReadCloser joins a Reader and a Closer into one body, such as an
// io.LimitReader over a counting body and the body itself, so that a bounded
// read still closes, and counts the close of, what it bounds.
type ReadCloser struct {
	io.Reader
	io.Closer
}
