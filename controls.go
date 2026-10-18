package treg

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Control bounds.
const (
	maxIdempotencyKeyBytes = 200
	maxTags                = 5
	maxTagKeyBytes         = 32
	maxTagValueBytes       = 128
	maxTagHeaderBytes      = 512
	maxCostMicro           = 1_000_000 * microPerUSD // $1,000,000
	maxProviders           = 32
	maxProviderBytes       = 64
	maxExtraValueBytes     = 1 << 10
	maxExtraNameSuffix     = 64
	maxExtraControls       = 16
)

// Controls are Treg's own request controls. Every field is optional, and the
// SDK writes each one the caller sets: it never drops one and never invents
// a default. Whether a control applies to a call is Treg's decision.
type Controls struct {
	// IdempotencyKey is sent as Idempotency-Key: 1 to 200 bytes of visible
	// ASCII. The key is yours: the SDK never invents, rotates or retries
	// one. Reuse a key only to recover the exact same request (Treg
	// fingerprints method, raw path, raw query and body, not headers).
	// Treg keeps keys for 24 hours, answers 409 while the first call runs,
	// 422 for a different request, and 410 when the stored answer was lost,
	// its outcome is unknown or it was too large to keep. Inline generative
	// media is regenerated and billed again on retry.
	IdempotencyKey string

	// MaxCost is sent as X-Treg-Route-Max-Cost, an exact decimal USD amount:
	// the reserve ceiling Treg enforces on a metered direct call and on a
	// routed call. 0 to $1,000,000; negative values are refused. The SDK
	// sets no default (Treg's routed default of $1.00 is its own policy).
	MaxCost *MicroUSD

	// Tags are sent as X-Treg-Meta: "k=v,k=v". At most 5 pairs with
	// distinct keys of 1 to 32 bytes of [a-z0-9_], values of 1 to 128 bytes
	// of [A-Za-z0-9._:-], and 512 bytes in all. A violation is refused,
	// never truncated.
	Tags []Tag

	// MaxAge is sent as X-Treg-Max-Age in whole seconds: accept an archived
	// answer only if it is younger. It must be whole seconds and not
	// negative.
	MaxAge *time.Duration

	// NoCache sends Cache-Control: no-cache, which forces a live call
	// instead of an archive hit, and may cost money.
	NoCache bool

	// Route holds the routed-call controls.
	Route RouteControls

	// Extra sends other X-Treg-* controls, for ones Treg adds after this SDK
	// release, such as X-Treg-Body-Encoding (with a body you have already
	// encoded). Names must be X-Treg- followed by 1 to 64 letters, digits
	// or hyphens; the token, org, client and authorization-method headers,
	// every control above and duplicates are refused. Values must be valid
	// header values of at most 1 KiB. At most 16.
	Extra []Control
}

// Tag is one X-Treg-Meta pair.
type Tag struct {
	Key   string
	Value string
}

// RouteControls are the controls of a routed call (a treg.* catalog id).
// Treg decides where each applies.
type RouteControls struct {
	// Waterfall sends X-Treg-Route-Waterfall: "1" or "0". Treg's default is
	// on: try the next provider after a miss.
	Waterfall *bool

	// StrictFilters sends X-Treg-Route-Strict-Filters: 1, so that Treg
	// answers 422, unbilled, instead of a looser match.
	StrictFilters bool

	// Verify sends X-Treg-Route-Verify: true. Only the email and phone find
	// capabilities accept it; others answer 422.
	Verify bool

	// Prefer sends X-Treg-Route-Prefer: a comma-separated provider list.
	// Provisional: it appears in descriptors, not in Treg's prose docs.
	Prefer []string

	// Exclude sends X-Treg-Route-Exclude: a comma-separated provider list.
	// Provisional, like Prefer.
	Exclude []string
}

// Control is one Extra header.
type Control struct {
	Name  string
	Value string
}

// apply validates the controls and writes their headers into h, adding
// every caller string to scan.
func (c Controls) apply(h http.Header, scan *scanList) error {
	if err := applyIdempotencyKey(c.IdempotencyKey, h, scan); err != nil {
		return err
	}
	if err := applyMaxCost(c.MaxCost, h); err != nil {
		return err
	}
	if len(c.Tags) > 0 {
		meta, err := encodeTags(c.Tags, scan)
		if err != nil {
			return err
		}
		h[headerMeta] = []string{meta}
	}
	if err := applyMaxAge(c.MaxAge, h); err != nil {
		return err
	}
	if c.NoCache {
		if err := applyNoCache(h); err != nil {
			return err
		}
	}
	if err := c.Route.apply(h, scan); err != nil {
		return err
	}
	return applyExtra(c.Extra, h, scan)
}

// shape describes which controls are set, for CallRequest.String.
func (c Controls) shape() string {
	var set []string
	if c.IdempotencyKey != "" {
		set = append(set, "idempotencyKey")
	}
	if c.MaxCost != nil {
		set = append(set, "maxCost")
	}
	if len(c.Tags) > 0 {
		set = append(set, "tags")
	}
	if c.MaxAge != nil {
		set = append(set, "maxAge")
	}
	if c.NoCache {
		set = append(set, "noCache")
	}
	if c.Route.Waterfall != nil || c.Route.StrictFilters || c.Route.Verify || len(c.Route.Prefer) > 0 ||
		len(c.Route.Exclude) > 0 {
		set = append(set, "route")
	}
	if len(c.Extra) > 0 {
		set = append(set, "extra")
	}
	return strings.Join(set, "+")
}

// applyIdempotencyKey validates the idempotency key, when set, and writes
// it into h under its non-canonical map key.
func applyIdempotencyKey(k string, h http.Header, scan *scanList) error {
	if k == "" {
		return nil
	}
	if reason := checkBounded(k, maxIdempotencyKeyBytes, isVisible); reason != "" {
		return invalidRequest("Controls.IdempotencyKey", reason)
	}
	h[headerIdempotencyKey] = []string{k}
	scan.add(k)
	return nil
}

// applyMaxCost validates the reserve ceiling, when set, and writes it into
// h as an exact decimal USD amount.
func applyMaxCost(m *MicroUSD, h http.Header) error {
	switch {
	case m == nil:
		return nil
	case *m < 0:
		return invalidRequest("Controls.MaxCost", reasonNegative)
	case *m > maxCostMicro:
		return invalidRequest("Controls.MaxCost", reasonOutOfRange)
	}
	h[headerMaxCost] = []string{m.DecimalUSD()}
	return nil
}

// encodeTags validates the tags and returns the X-Treg-Meta value.
func encodeTags(tags []Tag, scan *scanList) (string, error) {
	if len(tags) > maxTags {
		return "", invalidRequest("Controls.Tags", reasonTooMany)
	}
	var b strings.Builder
	for i, t := range tags {
		field := "Controls.Tags[" + strconv.Itoa(i) + "]"
		if reason := checkTagKey(t.Key); reason != "" {
			return "", invalidRequest(field+".Key", reason)
		}
		if reason := checkBounded(t.Value, maxTagValueBytes, isTagValueChar); reason != "" {
			return "", invalidRequest(field+".Value", reason)
		}
		if slices.ContainsFunc(tags[:i], func(prev Tag) bool { return prev.Key == t.Key }) {
			return "", invalidRequest(field+".Key", reasonDuplicate)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(t.Key)
		b.WriteByte('=')
		b.WriteString(t.Value)
		scan.add(t.Value)
	}
	if b.Len() > maxTagHeaderBytes {
		return "", invalidRequest("Controls.Tags", reasonTooLarge)
	}
	return b.String(), nil
}

// checkTagKey returns "" for a tag key matching ^[a-z0-9_]{1,32}$, else a
// reason.
func checkTagKey(k string) string { return checkBounded(k, maxTagKeyBytes, isCodeChar) }

// applyMaxAge validates the archive age bound, when set, and writes it into
// h in whole seconds.
func applyMaxAge(d *time.Duration, h http.Header) error {
	switch {
	case d == nil:
		return nil
	case *d < 0:
		return invalidRequest("Controls.MaxAge", reasonNegative)
	case *d%time.Second != 0:
		return invalidRequest("Controls.MaxAge", reasonNotWholeSeconds)
	}
	h[headerMaxAge] = []string{strconv.FormatInt(int64(*d/time.Second), 10)}
	return nil
}

// applyNoCache writes Cache-Control: no-cache into h. A Cache-Control the
// caller set in CallRequest.Header, under any spelling, conflicts.
func applyNoCache(h http.Header) error {
	for range foldedValues(h, headerCacheControl) {
		return invalidRequest("Controls.NoCache", reasonConflict)
	}
	h[headerCacheControl] = []string{"no-cache"}
	return nil
}

// apply validates the routed controls and writes their headers.
func (r RouteControls) apply(h http.Header, scan *scanList) error {
	if r.Waterfall != nil {
		v := "0"
		if *r.Waterfall {
			v = "1"
		}
		h[headerWaterfall] = []string{v}
	}
	if r.StrictFilters {
		h[headerStrictFilters] = []string{"1"}
	}
	if r.Verify {
		h[headerVerify] = []string{"true"}
	}
	for _, list := range [...]struct {
		field, header string
		values        []string
	}{
		{"Controls.Route.Prefer", headerPrefer, r.Prefer},
		{"Controls.Route.Exclude", headerExclude, r.Exclude},
	} {
		if len(list.values) == 0 {
			continue
		}
		if len(list.values) > maxProviders {
			return invalidRequest(list.field, reasonTooMany)
		}
		for i, p := range list.values {
			if reason := checkProvider(p); reason != "" {
				return invalidRequest(list.field+"["+strconv.Itoa(i)+"]", reason)
			}
		}
		scan.add(list.values...)
		h[list.header] = []string{strings.Join(list.values, ",")}
	}
	return nil
}

// checkProvider returns "" for a provider service name, 1 to 64 bytes of
// letters, digits and "._-", else a reason.
func checkProvider(p string) string { return checkBounded(p, maxProviderBytes, isProviderChar) }

// validProvider reports whether checkProvider accepts p.
func validProvider(p string) bool { return boundedOf(p, maxProviderBytes, isProviderChar) }

// applyExtra validates the Extra controls and writes them into h.
func applyExtra(extra []Control, h http.Header, scan *scanList) error {
	if len(extra) > maxExtraControls {
		return invalidRequest("Controls.Extra", reasonTooMany)
	}
	for i, c := range extra {
		field := "Controls.Extra[" + strconv.Itoa(i) + "]"
		if reason := checkExtraName(c.Name); reason != "" {
			return invalidRequest(field+".Name", reason)
		}
		if refusedExtra(c.Name) {
			return invalidRequest(field+".Name", reasonReservedHeader)
		}
		if slices.ContainsFunc(extra[:i], func(prev Control) bool { return strings.EqualFold(prev.Name, c.Name) }) {
			return invalidRequest(field+".Name", reasonDuplicate)
		}
		if reason := checkHeaderValue(c.Value, maxExtraValueBytes); reason != "" {
			return invalidRequest(field+".Value", reason)
		}
		h[http.CanonicalHeaderKey(c.Name)] = []string{c.Value}
		scan.add(c.Value)
	}
	return nil
}

// checkExtraName returns "" for an Extra name, "X-Treg-" (any case) followed
// by 1 to 64 letters, digits or hyphens, else a reason. A name outside the
// X-Treg- namespace, or with nothing after the prefix, is invalid_format.
func checkExtraName(name string) string {
	switch {
	case name == "":
		return reasonRequired
	case !hasPrefixFold(name, tregHeaderPrefix) || len(name) == len(tregHeaderPrefix):
		return reasonInvalidFormat
	}
	return checkBounded(name[len(tregHeaderPrefix):], maxExtraNameSuffix, isLDH)
}

// refusedExtra reports whether name, compared ignoring case, is an X-Treg-*
// header Extra may not set: authentication, attribution, OAuth grant
// selection, or a typed control.
func refusedExtra(name string) bool {
	for _, refused := range [...]string{
		headerToken, headerOrg, headerClient, headerAuthorizationMethod, headerMaxCost, headerMeta,
		headerMaxAge, headerWaterfall, headerStrictFilters, headerVerify, headerPrefer, headerExclude,
	} {
		if strings.EqualFold(name, refused) {
			return true
		}
	}
	return false
}
