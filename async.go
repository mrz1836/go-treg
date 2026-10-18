package treg

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"
)

// errNotObject reports a JSON value that is not an object where one is
// required.
var errNotObject = errors.New("treg: value is not a JSON object")

// AsyncDescriptor is Treg's X-Treg-Async header: how to follow an
// asynchronous submission. It is descriptor data from the response, never
// acted on by the SDK: the SDK does not poll, fetch or interpret it.
// Following it is the caller's own explicit code.
//
// On a direct catalog call it is the catalog row's descriptor; on a routed
// 202 it carries the child's TaskID, Poll and Status. Members the SDK does
// not model stay in Raw, which holds the header's exact bytes.
type AsyncDescriptor struct {
	// IDFrom is a dotted path into the submission response where the task
	// id is, such as "id" or "data.task_id".
	IDFrom string

	// TaskID is the task id, on a routed 202 only.
	TaskID string

	// Poll says where to poll.
	Poll AsyncPoll

	// Status says which status values mean done or failed.
	Status AsyncStatus

	// Result says where the result is.
	Result AsyncResult

	// Interval is the minimum time between polls, in seconds.
	Interval Decimal

	// MaxAge is how long, in seconds, to keep polling before a task counts
	// as abandoned (and is refunded).
	MaxAge Optional[int64]

	// TerminalOnSubmission reports that a submission whose status is already
	// a success is final.
	TerminalOnSubmission bool

	// Raw is the descriptor exactly as received.
	Raw json.RawMessage
}

// AsyncPoll is the poll part of an AsyncDescriptor: either a catalog
// endpoint plus the parameter that takes the task id (Endpoint and Param),
// or a URL taken from the submission response (URLFrom), whose host must be
// one of URLHosts.
type AsyncPoll struct {
	Endpoint EndpointID
	Param    *AsyncParam
	URLFrom  string
	URLHosts []string
}

// AsyncParam names a request parameter: In is "pathParams" or
// "queryParams". Path parameters of a catalog endpoint ride in the query.
type AsyncParam struct {
	In   string
	Name string
}

// AsyncStatus is the status part of an AsyncDescriptor: Path is a dotted
// path into the poll response, and each list holds JSON values (strings or
// numbers). A value in none of them is not a success.
type AsyncStatus struct {
	Path          string
	Success       []json.RawMessage
	Failure       []json.RawMessage
	BilledFailure []json.RawMessage
	Progress      []json.RawMessage
}

// AsyncResult is the result part of an AsyncDescriptor: either a dotted
// Path into the terminal poll response, or a catalog endpoint to Fetch with
// FetchParam. TTLNote is free text about when result URLs expire.
type AsyncResult struct {
	Path       string
	TTLNote    string
	Fetch      string
	FetchParam *AsyncFetchParam
}

// AsyncFetchParam names the parameter of a result fetch and the dotted path
// its value comes from.
type AsyncFetchParam struct {
	In        string
	Name      string
	ValueFrom string
}

// UnmarshalJSON decodes an async descriptor, as Metadata.Async decodes the
// X-Treg-Async header.
func (d *AsyncDescriptor) UnmarshalJSON(b []byte) error {
	if d == nil {
		return errNilTarget
	}
	parsed, err := parseAsyncDescriptor(b)
	if err != nil {
		return err
	}
	*d = *parsed
	return nil
}

// MarshalJSON encodes Raw.
func (d AsyncDescriptor) MarshalJSON() ([]byte, error) {
	if len(d.Raw) == 0 || !json.Valid(d.Raw) {
		return []byte("null"), nil
	}
	return d.Raw, nil
}

// parseAsyncDescriptor decodes an X-Treg-Async value. It must be one JSON
// object; members of an unexpected type are left unset rather than failing
// the whole descriptor.
func parseAsyncDescriptor(b []byte) (*AsyncDescriptor, error) {
	members, err := objectMembers(b)
	if err != nil {
		return nil, err
	}
	d := &AsyncDescriptor{Raw: json.RawMessage(bytes.Clone(members.b))}
	decodeMember(members, "id_from", &d.IDFrom)
	decodeMember(members, "task_id", &d.TaskID)
	decodeMember(members, "max_age", &d.MaxAge)
	decodeMember(members, "terminal_on_submission", &d.TerminalOnSubmission)
	decodeMember(members, "interval", &d.Interval)
	if poll, ok := members.member("poll"); ok {
		d.Poll = parseAsyncPoll(poll)
	}
	if status, ok := members.member("status"); ok {
		d.Status = parseAsyncStatus(status)
	}
	if result, ok := members.member("result"); ok {
		d.Result = parseAsyncResult(result)
	}
	return d, nil
}

// parseAsyncPoll decodes the poll member leniently.
func parseAsyncPoll(b json.RawMessage) AsyncPoll {
	var p AsyncPoll
	m, err := objectMembers(b)
	if err != nil {
		return p
	}
	decodeMember(m, "endpoint", &p.Endpoint)
	decodeMember(m, "url_from", &p.URLFrom)
	decodeMember(m, "url_hosts", &p.URLHosts)
	if raw, ok := m.member("param"); ok {
		if pm, perr := objectMembers(raw); perr == nil {
			p.Param = new(AsyncParam)
			decodeMember(pm, "in", &p.Param.In)
			decodeMember(pm, "name", &p.Param.Name)
		}
	}
	return p
}

// parseAsyncStatus decodes the status member leniently.
func parseAsyncStatus(b json.RawMessage) AsyncStatus {
	var s AsyncStatus
	m, err := objectMembers(b)
	if err != nil {
		return s
	}
	decodeMember(m, "path", &s.Path)
	decodeMember(m, "success", &s.Success)
	decodeMember(m, "failure", &s.Failure)
	decodeMember(m, "billed_failure", &s.BilledFailure)
	decodeMember(m, "progress", &s.Progress)
	return s
}

// parseAsyncResult decodes the result member leniently.
func parseAsyncResult(b json.RawMessage) AsyncResult {
	var r AsyncResult
	m, err := objectMembers(b)
	if err != nil {
		return r
	}
	decodeMember(m, "path", &r.Path)
	decodeMember(m, "ttl_note", &r.TTLNote)
	decodeMember(m, "fetch", &r.Fetch)
	if raw, ok := m.member("fetch_param"); ok {
		if fm, ferr := objectMembers(raw); ferr == nil {
			r.FetchParam = new(AsyncFetchParam)
			decodeMember(fm, "in", &r.FetchParam.In)
			decodeMember(fm, "name", &r.FetchParam.Name)
			decodeMember(fm, "value_from", &r.FetchParam.ValueFrom)
		}
	}
	return r
}

// jsonObject is one valid JSON object, b, with the key (quotes included)
// and the value of each member, in order. Keys and values are slices of b.
type jsonObject struct {
	b       []byte
	members []jsonMember
}

// objectMembersHint is the member count a jsonObject has room for before
// its index grows: more than the objects the SDK reads this way hold.
const objectMembersHint = 8

// jsonMember is one member of a jsonObject.
type jsonMember struct {
	key, value []byte
}

// objectMembers reads b, without surrounding white space, as a jsonObject,
// in one pass and without copying a key or value. It returns errNotObject
// unless b is one valid JSON object.
func objectMembers(b []byte) (jsonObject, error) {
	b = bytes.TrimSpace(b)
	if !isJSONObject(b) || !json.Valid(b) {
		return jsonObject{}, errNotObject
	}
	o, ok := indexObject(b)
	if !ok {
		return jsonObject{}, errNotObject
	}
	return o, nil
}

// indexObject finds the members of b, which must be one valid JSON object
// without surrounding white space. It reports false if the walk fails.
func indexObject(b []byte) (jsonObject, bool) {
	o := jsonObject{b: b, members: make([]jsonMember, 0, objectMembersHint)}
	s := strictScanner{b: b, i: 1}
	for {
		s.space()
		if s.i >= len(s.b) || s.b[s.i] != '"' {
			return o, true // the closing brace
		}
		keyStart := s.i
		if _, err := s.str(false); err != nil {
			return jsonObject{}, false
		}
		key := s.b[keyStart:s.i]
		s.space()
		s.i++ // the colon
		s.space()
		valueStart := s.i
		if err := s.value(false, 1); err != nil {
			return jsonObject{}, false
		}
		o.members = append(o.members, jsonMember{key: key, value: s.b[valueStart:s.i]})
		s.space()
		if s.i >= len(s.b) || s.b[s.i] != ',' {
			return o, true
		}
		s.i++
	}
}

// member returns the value of the last member whose key decodes to key,
// exactly as decoding o into a map[string]json.RawMessage and looking key
// up would. The value is a slice of o, not a copy.
func (o jsonObject) member(key string) (json.RawMessage, bool) {
	for _, m := range slices.Backward(o.members) {
		if keyIs(m.key, key) {
			return json.RawMessage(m.value), true
		}
	}
	return nil, false
}

// keyIs reports whether the JSON string raw, quotes included, decodes to
// key. Only a key with an escape or invalid UTF-8 is decoded, by
// encoding/json; any other key's text is its value.
func keyIs(raw []byte, key string) bool {
	text := raw[1 : len(raw)-1]
	if bytes.IndexByte(text, '\\') < 0 && utf8.Valid(text) {
		return string(text) == key
	}
	var decoded string
	return json.Unmarshal(raw, &decoded) == nil && decoded == key
}

// decodeMember decodes the member key of o into v and reports whether it
// did. A missing member or one of another type leaves v as it was.
func decodeMember(o jsonObject, key string, v any) bool {
	raw, ok := o.member(key)
	if !ok {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}
