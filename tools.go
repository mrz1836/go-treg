package treg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// ToolsService reads the team's registered tools: GET /tools and
// /tools/by-name/{name}. Get it from Client.Tools; the zero ToolsService
// returns ErrInvalidConfig.
type ToolsService struct {
	c *Client
}

// Tools returns the tool reads.
func (c *Client) Tools() ToolsService { return ToolsService{c: c} }

// ListToolsRequest is the request of ToolsService.List.
type ListToolsRequest struct{}

// Tool is a tool registered in the team. Bindings name the secrets Treg
// injects (secret ids, never secret values), and Owner is the creator's
// email, so a Tool prints only its id, name and binding count.
type Tool struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	Owner          string          `json:"owner"`
	BaseURL        string          `json:"base_url"`
	Host           string          `json:"host"`
	Bindings       []ToolBinding   `json:"bindings"`
	HealthCheck    json.RawMessage `json:"health_check,omitempty"`
	Examples       json.RawMessage `json:"examples,omitempty"`
	CLI            json.RawMessage `json:"cli,omitempty"`
	ServerRunnable bool            `json:"server_runnable"`
	ProjectID      Optional[int64] `json:"project_id,omitzero"` // null: org-wide
	BundleID       Optional[int64] `json:"bundle_id,omitzero"`
}

// ToolBinding says how Treg injects one secret into a tool's requests.
type ToolBinding struct {
	SecretID    json.RawMessage  `json:"secret_id"`
	Injector    Optional[string] `json:"injector,omitzero"`
	Location    Optional[string] `json:"location,omitzero"`
	Name        Optional[string] `json:"name,omitzero"`
	ValueFormat Optional[string] `json:"format,omitzero"`
	SecretField Optional[string] `json:"secret_field,omitzero"`
	TokenEncode Optional[string] `json:"token_encode,omitzero"`
}

// String prints the id, the name and the binding count only.
func (t Tool) String() string {
	name := t.Name
	if checkName(name) != "" {
		name = "?"
	}
	return "treg.Tool{id: " + strconv.FormatInt(t.ID, 10) + ", name: " + name +
		", bindings: " + strconv.Itoa(len(t.Bindings)) + "}"
}

// GoString returns the same text as String.
func (t Tool) GoString() string { return t.String() }

// Format prints String for every verb.
func (t Tool) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, t.String()) }

// MarshalJSON encodes the id and the binding count only.
func (t Tool) MarshalJSON() ([]byte, error) {
	return json.Marshal(shapeJSON{Type: "Tool", ID: &t.ID, Count: new(len(t.Bindings))})
}

// String prints nothing but the type: a binding names a secret.
func (ToolBinding) String() string { return "treg.ToolBinding{}" }

// GoString returns the same text as String.
func (b ToolBinding) GoString() string { return b.String() }

// Format prints String for every verb.
func (b ToolBinding) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, b.String()) }

// MarshalJSON encodes the type only.
func (ToolBinding) MarshalJSON() ([]byte, error) { return json.Marshal(shapeJSON{Type: "ToolBinding"}) }

// List reads GET /tools, filtered by the caller's access. It accepts the
// CaptureResponse option.
func (s ToolsService) List(ctx context.Context, req ListToolsRequest, opts ...CallOption) (*Result[[]Tool], error) {
	return read[[]Tool](ctx, s.c.reader(), req.route, opts)
}

// route returns the request of ToolsService.List.
func (ListToolsRequest) route() (readRequest, error) {
	return readRequest{op: OpListTools, rawPath: "/tools"}, nil
}

// ToolByNameRequest is the request of ToolsService.ByName.
type ToolByNameRequest struct {
	// Name is the tool's name, with the shape RegisteredTool requires.
	Name string
}

// ByName reads GET /tools/by-name/{name}. It accepts the CaptureResponse
// option.
func (s ToolsService) ByName(ctx context.Context, req ToolByNameRequest, opts ...CallOption) (*Result[Tool], error) {
	return read[Tool](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of ToolsService.ByName.
func (r ToolByNameRequest) route() (readRequest, error) {
	var q readQuery
	q.segment("Name", r.Name, checkName)
	return q.get(OpToolByName, "/tools/by-name/"+r.Name)
}
