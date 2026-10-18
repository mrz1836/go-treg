package treg

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"sync/atomic"
)

// Find bounds.
const (
	maxFindLineBytes  = 1 << 20
	maxFindTotalBytes = 16 << 20
	findReadChunk     = 32 << 10
	ndjsonContentType = "application/x-ndjson"
)

// FindRequest is the request of CatalogClient.Find.
type FindRequest struct {
	// Query is what to find: 1 to 500 bytes of UTF-8 without control
	// characters. Required.
	Query string

	// Platform narrows the search to one platform slug. Optional.
	Platform string
}

// FindEventType names a FindEvent: "candidates" or "judged". The set is
// open; Known reports a listed value.
type FindEventType string

// Find event types.
const (
	FindCandidates FindEventType = "candidates"
	FindJudged     FindEventType = "judged"
)

// Known reports whether t is one of the listed event types.
func (t FindEventType) Known() bool { return t == FindCandidates || t == FindJudged }

// FindEvent is one line of the Find stream. A "candidates" event lists
// candidate rows (it may come more than once); the "judged" event carries
// Treg's verdict and ranked rows. Members this release does not model stay
// in Raw, the line exactly as received.
type FindEvent struct {
	Event      FindEventType     `json:"event"`
	Candidates []FindCandidate   `json:"candidates,omitempty"`
	Verdict    Optional[string]  `json:"verdict,omitzero"`
	Named      Optional[string]  `json:"named,omitzero"`
	Read       Optional[int64]   `json:"read,omitzero"`
	High       Optional[Decimal] `json:"high,omitzero"`
	Rows       []FindRow         `json:"rows,omitempty"`
	Reason     Optional[string]  `json:"reason,omitzero"`
	Engine     Optional[string]  `json:"engine,omitzero"`

	// Raw is the event's line as received.
	Raw json.RawMessage `json:"-"`
}

// FindCandidate is one candidate row.
type FindCandidate struct {
	ID       EndpointID      `json:"id"`
	Platform string          `json:"platform"`
	Provider string          `json:"provider"`
	Units    json.RawMessage `json:"units,omitempty"`
}

// FindRow is one judged row. P is Treg's fit score.
type FindRow struct {
	ID                    EndpointID        `json:"id"`
	Name                  string            `json:"name"`
	Provider              string            `json:"provider"`
	ProviderDisplay       string            `json:"provider_display"`
	Capability            string            `json:"capability"`
	CapabilityDescription string            `json:"capability_description"`
	Platform              string            `json:"platform"`
	PlatformLabel         string            `json:"platform_label"`
	Cost                  Optional[Cost]    `json:"cost,omitzero"`
	P                     Optional[Decimal] `json:"p,omitzero"`
	FitFrom               Optional[string]  `json:"fit_from,omitzero"`
	ChildrenHidden        json.RawMessage   `json:"children_hidden,omitempty"`
}

// FindStream is the open NDJSON stream of CatalogClient.Find. Range over
// Events once; the stream closes when the loop ends, and Close may be
// called at any time. Its life is bounded by WithStreamTimeout, including
// the time the caller's loop takes; its header wait by the lower of
// WithTimeout and WithStreamTimeout; each line by 1 MiB and the whole body
// by 16 MiB.
type FindStream struct {
	// stream returns the open stream, behind a function value like
	// Response's.
	stream   sealed[*stream]
	consumed atomic.Bool
}

// Find opens GET /catalog/find, Treg's judged search: it validates the
// request, makes one GET and returns the stream. A non-2xx answer is a
// *CallError, as for the typed reads. It takes no CallOption because it is
// a stream: the typed reads' one option, CaptureResponse, keeps a buffered
// body, and a stream buffers none. Its local checks are the typed reads'
// own, in their order: the client, the request, the context, then the URL
// bound and the credential scan.
func (c *CatalogClient) Find(ctx context.Context, req FindRequest) (*FindStream, error) {
	r := c.reader()
	_, w, err := r.prepare(ctx, req.route, nil)
	if err != nil {
		return nil, err
	}
	st, ce := r.cfg.openStream(ctx, w, maxFindTotalBytes)
	if ce != nil {
		return nil, ce
	}
	if err := checkFindResponse(ctx, st); err != nil {
		return nil, err
	}
	return &FindStream{stream: seal(st)}, nil
}

// route validates the request of CatalogClient.Find.
func (r FindRequest) route() (readRequest, error) {
	var q readQuery
	q.refuse(r.Query == "", "Query", reasonRequired)
	q.text("Query", "q", r.Query, maxQueryBytes)
	q.check("Platform", "platform", r.Platform, checkName)
	rr, err := q.get(OpCatalogFind, catalogPathRoot+"find")
	rr.accept = ndjsonContentType
	return rr, err
}

// checkFindResponse refuses a non-2xx or an undecodable 2xx, closing the
// stream.
func checkFindResponse(ctx context.Context, st *stream) error {
	if st.status < 200 || st.status >= 300 {
		err := newResponse(st).StatusError(ctx)
		_ = st.close() // StatusError leaves a 3xx open; Find drops the stream either way
		return err
	}
	fail := func(reason string, cause error) error {
		_ = st.close()
		return newCallError(st.op, FailureUnexpectedResponse, reason, st.status, cause).withResponse(st.meta, nil)
	}
	if encoded(st.header) {
		return fail(reasonContentEncoding, errContentEncoding)
	}
	mt, ok := mediaType(st.header)
	switch {
	case !ok:
		return fail(reasonContentType, errContentType)
	case mt == "", mt == ndjsonContentType, mt == "application/ndjson", mt == "application/jsonl",
		mt == "application/x-jsonlines", mt == jsonContentType:
		return nil
	}
	return fail(reasonContentType, errContentType)
}

// Meta returns the response metadata.
func (s *FindStream) Meta() Metadata {
	if st := s.open(); st != nil {
		return st.meta
	}
	return Metadata{}
}

// Close ends the stream. It is idempotent; the zero FindStream returns
// ErrInvalidConfig.
func (s *FindStream) Close() error {
	st := s.open()
	if st == nil {
		return notInitialized("stream")
	}
	return st.close()
}

// Events returns the stream's events, in order. It is single-use: a second
// range yields one ErrInvalidRequest (reason "stream_consumed"). A line that
// is not a valid event, a line over 1 MiB, a body over 16 MiB, a transport
// failure or a timeout yields one *CallError and ends the loop. The stream
// closes when the loop ends, early break included.
func (s *FindStream) Events() iter.Seq2[FindEvent, error] {
	return func(yield func(FindEvent, error) bool) {
		st := s.open()
		if st == nil {
			yield(FindEvent{}, notInitialized("stream"))
			return
		}
		if !s.consumed.CompareAndSwap(false, true) {
			yield(FindEvent{}, invalidRequest("FindStream", reasonStreamConsumed))
			return
		}
		defer func() { _ = st.close() }()
		s.run(st, yield)
	}
}

// open returns the stream, or nil for the zero FindStream.
func (s *FindStream) open() *stream {
	if s == nil || s.stream == nil {
		return nil
	}
	return s.stream()
}

// run reads lines and yields their events until the body ends, a failure
// or the caller stops.
func (s *FindStream) run(st *stream, yield func(FindEvent, error) bool) {
	var pending []byte
	buf := make([]byte, findReadChunk)
	for {
		n, err := st.read(buf)
		pending = append(pending, buf[:n]...)
		for {
			i := bytes.IndexByte(pending, '\n')
			if i < 0 {
				break
			}
			line := pending[:i]
			pending = pending[i+1:]
			if !s.emit(st, line, yield) {
				return
			}
		}
		if len(pending) > maxFindLineBytes {
			yield(FindEvent{}, lineFailure(st, reasonLineTooLong, errResponseTooLarge, FailureResponseTooLarge))
			return
		}
		switch {
		case err == io.EOF: //nolint:errorlint // stream.read returns io.EOF itself
			if len(bytes.TrimSpace(pending)) > 0 {
				s.emit(st, pending, yield)
			}
			return
		case err != nil:
			yield(FindEvent{}, err)
			return
		}
	}
}

// emit decodes one line and yields its event, reporting whether to go on.
// Blank lines are skipped.
func (s *FindStream) emit(st *stream, line []byte, yield func(FindEvent, error) bool) bool {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return true
	}
	if len(line) > maxFindLineBytes {
		yield(FindEvent{}, lineFailure(st, reasonLineTooLong, errResponseTooLarge, FailureResponseTooLarge))
		return false
	}
	ev, df := decodeFindEvent(line)
	if df != nil {
		yield(FindEvent{}, lineFailure(st, df.reason, df.cause, df.kind))
		return false
	}
	return yield(ev, nil)
}

// lineFailure returns the CallError for a bad line of st.
func lineFailure(st *stream, reason string, cause error, kind FailureKind) *CallError {
	return newCallError(st.op, kind, reason, st.status, cause).withResponse(st.meta, nil)
}

// decodeFindEvent decodes one line under the strict rules for SDK models.
func decodeFindEvent(line []byte) (FindEvent, *decodeFailure) {
	if !isJSONObject(line) {
		return FindEvent{}, &decodeFailure{FailureMalformedResponse, reasonInvalidJSON, errInvalidJSON}
	}
	if df := strictFailure(line, rawIsland); df != nil {
		return FindEvent{}, df
	}
	var ev FindEvent
	if df := decodeValue(line, &ev, false); df != nil {
		return FindEvent{}, df
	}
	ev.Raw = json.RawMessage(bytes.Clone(line))
	return ev, nil
}
