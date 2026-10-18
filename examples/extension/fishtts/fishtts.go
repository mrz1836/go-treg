// Package fishtts is an example of an external adapter for an endpoint
// unlike a JSON lookup: the Treg catalog endpoint "fishaudio.tts.s2-1-pro"
// (Fish Audio text to speech) takes a JSON body plus a required provider
// header, "model: s2.1-pro", and answers with binary audio such as
// audio/mpeg.
//
// So it streams: Synthesize uses Client.Call, checks the status with
// Response.StatusError, checks that the answer is audio, and copies it into
// the caller's io.Writer without buffering the whole file. Like the
// urlmetrics example, it lives outside go-treg, uses only the public API,
// owns its types and adds no dependency. It never sees the Treg token: it
// receives a *treg.Client.
//
// The endpoint is metered by the UTF-8 bytes of the text. MaxTextBytes is
// this package's own guard against an accidental large bill, not a provider
// limit. The descriptor's input has more fields (prosody, a list of
// reference ids, and others); this package models a few and sends nothing
// it does not model.
package fishtts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/mrz1836/go-treg"
)

const (
	// EndpointID is the catalog id this package calls.
	EndpointID = "fishaudio.tts.s2-1-pro"

	// Model is the value of the provider header the descriptor requires
	// (input.headers.model).
	Model = "s2.1-pro"

	// MaxTextBytes is this package's bound on Request.Text, in bytes.
	MaxTextBytes = 10_000
)

// Errors of this package. ErrText, ErrFormat and ErrNoWriter mean nothing
// was sent. ErrNotAudio and ErrWrite come after a 2xx arrived: the call
// executed and may have been billed, so do not resubmit it.
var (
	// ErrText reports a Text that is empty, only whitespace, longer than
	// MaxTextBytes or not valid UTF-8.
	ErrText = errors.New("fishtts: text must be 1 to 10000 bytes of UTF-8")

	// ErrFormat reports a Format other than "", "mp3", "wav", "pcm" or
	// "opus".
	ErrFormat = errors.New("fishtts: format must be mp3, wav, pcm or opus")

	// ErrNoWriter reports a nil destination.
	ErrNoWriter = errors.New("fishtts: nil destination writer")

	// ErrNotAudio reports a 2xx whose Content-Type is neither audio/* nor
	// application/octet-stream (raw PCM may come as the latter), such as a
	// JSON body. Nothing was written to the destination; the call executed.
	ErrNotAudio = errors.New("fishtts: response is not audio")

	// ErrWrite reports that the destination refused audio after the
	// response started. The error also wraps the destination's own error.
	ErrWrite = errors.New("fishtts: writing audio to the destination failed")
)

// Request is the JSON body of fishaudio.tts.s2-1-pro.
type Request struct {
	// Text is read aloud: 1 to MaxTextBytes bytes of UTF-8. Required.
	Text string `json:"text"`

	// ReferenceID selects a voice model. The provider also accepts a list;
	// this package sends one id or none.
	ReferenceID string `json:"reference_id,omitempty"`

	// Format is "mp3", "wav", "pcm" or "opus"; empty means the provider's
	// default.
	Format string `json:"format,omitempty"`

	// Normalize, when set, is sent as given, false included.
	Normalize *bool `json:"normalize,omitempty"`

	// Latency is sent as given, when set. It changes how the audio is
	// produced, not what comes back, so the provider validates it.
	Latency string `json:"latency,omitempty"`
}

// Result describes the audio a call delivered.
type Result struct {
	// ContentType is the response's media type, without parameters, such as
	// "audio/mpeg". It is "" when the header is missing or malformed.
	ContentType string

	// Bytes counts the audio bytes written to the destination.
	Bytes int64

	// Meta is Treg's response metadata: the call ref and the cost evidence.
	// It is set whenever a response arrived, even with an error.
	Meta treg.Metadata
}

// Validate applies this package's rules to r, before anything is sent. It
// returns ErrText, ErrFormat or nil.
func (r Request) Validate() error {
	if r.Text == "" || len(r.Text) > MaxTextBytes || strings.TrimSpace(r.Text) == "" || !utf8.ValidString(r.Text) {
		return ErrText
	}
	switch r.Format {
	case "", "mp3", "wav", "pcm", "opus":
		return nil
	}
	return ErrFormat
}

// Synthesize makes one POST to fishaudio.tts.s2-1-pro and streams the audio
// into dst.
//
// This function performs the following steps:
//   - validate in and dst; nothing is sent when either is wrong
//   - send the request through Client.Call with the "model" header
//   - return the *treg.CallError of a 4xx or 5xx from Response.StatusError
//   - refuse a 2xx that is not audio/* or application/octet-stream, without
//     writing to dst
//   - copy the body into dst and close the response
//
// Parameters:
//   - ctx: bounds the whole call, the copy included; canceling it stops the
//     stream
//   - c: a Client from treg.NewClient. Its WithMaxStreamBytes bounds the
//     audio size and its WithStreamTimeout the whole stream
//   - in: the request, checked with Validate
//   - dst: where the audio goes, written in order as it arrives
//   - ctl: Treg's controls, passed through unchanged. An IdempotencyKey does
//     not make a retry free here: Treg regenerates and bills inline
//     generative media again
//
// Returns:
//   - the Result, whose Meta is set whenever a response arrived
//   - ErrText, ErrFormat or ErrNoWriter, or a *treg.ValidationError: nothing
//     was sent
//   - a *treg.CallError before or with a non-2xx response. A 5xx matches
//     treg.ErrOutcomeUnknown: reconcile through Client.Calls().Get first
//   - after a 2xx: ErrNotAudio; ErrWrite when dst fails; or a *treg.CallError
//     from reading the body (too large, timed out, canceled, cut off). The
//     call executed and may have been billed: do not resubmit it, and use
//     Result.Bytes to see how much audio reached dst
func Synthesize(ctx context.Context, c *treg.Client, in Request, dst io.Writer, ctl treg.Controls) (Result, error) {
	if err := in.Validate(); err != nil {
		return Result{}, err
	}
	if dst == nil {
		return Result{}, ErrNoWriter
	}
	resp, err := c.Call(ctx, treg.CallRequest{
		Target:   treg.CatalogEndpoint(EndpointID),
		Method:   http.MethodPost,
		Header:   http.Header{"Model": {Model}},
		Body:     treg.JSONBody(in),
		Controls: ctl,
	})
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Close() }()
	res := Result{Meta: resp.Meta()}
	if err = resp.StatusError(ctx); err != nil {
		return res, err
	}
	if mt, _, perr := mime.ParseMediaType(resp.Header().Get("Content-Type")); perr == nil {
		res.ContentType = mt
	}
	if !strings.HasPrefix(res.ContentType, "audio/") && res.ContentType != "application/octet-stream" {
		return res, ErrNotAudio
	}
	res.Bytes, err = copyAudio(dst, resp)
	return res, err
}

// copyAudio copies src into dst until src ends, and returns the bytes dst
// accepted. A read error is returned as it is, a *treg.CallError for a
// Response; a write error is wrapped in ErrWrite, so the two stay apart.
func copyAudio(dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			if w < 0 || w > n {
				w = 0
			}
			total += int64(w)
			if werr == nil && w != n {
				werr = io.ErrShortWrite
			}
			if werr != nil {
				return total, fmt.Errorf("%w: %w", ErrWrite, werr)
			}
		}
		if errors.Is(rerr, io.EOF) {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
