// Package slskd is a Go client for the slskd REST API (v0): search, response
// fetching, enqueue, transfer polling, and cancellation.
package slskd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one slskd instance over HTTP.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New creates a client for the slskd instance at baseURL.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// APIError is a non-2xx response from slskd.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("slskd: HTTP %d: %s", e.Status, e.Body)
}

// ---- Search ----

type SearchRequest struct {
	SearchText    string `json:"searchText"`
	SearchTimeout int    `json:"searchTimeout"` // ms
}

type SearchState struct {
	ID          string `json:"id"`
	SearchText  string `json:"searchText"`
	State       string `json:"state"`
	IsComplete  bool   `json:"isComplete"`
	ResponseCount int  `json:"responseCount"`
	FileCount   int    `json:"fileCount"`
	LockedFileCount int `json:"lockedFileCount"`
}

// StartSearch begins a search and returns its ID. slskd <0.26 returns the
// bare id string; 0.26+ returns the full search object.
func (c *Client) StartSearch(ctx context.Context, searchText string, timeoutMS int) (string, error) {
	body, _ := json.Marshal(SearchRequest{SearchText: searchText, SearchTimeout: timeoutMS})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v0/searches", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", &APIError{Status: resp.StatusCode, Body: string(b)}
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// try bare string first
	var id string
	if err := json.Unmarshal(raw, &id); err == nil && id != "" {
		return id, nil
	}
	// else full object
	var obj SearchState
	if err := json.Unmarshal(raw, &obj); err == nil && obj.ID != "" {
		return obj.ID, nil
	}
	return "", fmt.Errorf("slskd: unexpected search response: %.200s", raw)
}

// GetSearchState returns the current state of a search.
func (c *Client) GetSearchState(ctx context.Context, id string) (*SearchState, error) {
	var st SearchState
	err := c.do(ctx, http.MethodGet, "/api/v0/searches/"+url.PathEscape(id), nil, &st)
	return &st, err
}

// ---- Search responses ----

// File is one file in a peer's search response.
type File struct {
	Filename   string  `json:"filename"`
	Size       int64   `json:"size"`
	BitRate    *int    `json:"bitRate"`
	BitDepth   *int    `json:"bitDepth"`
	SampleRate *int    `json:"sampleRate"`
	Length     *float64 `json:"length"`
	Extension  string  `json:"extension"`
}

// UserResponse is one peer's response to a search.
type UserResponse struct {
	Username         string `json:"username"`
	HasFreeUploadSlot bool  `json:"hasFreeUploadSlot"`
	UploadSpeed      int64  `json:"uploadSpeed"`
	QueueLength      int    `json:"queueLength"`
	Files            []File `json:"files"`
	LockedFiles      []File `json:"lockedFiles"`
}

// GetSearchResponses fetches all peer responses for a search.
func (c *Client) GetSearchResponses(ctx context.Context, id string) ([]UserResponse, error) {
	var rs []UserResponse
	err := c.do(ctx, http.MethodGet, "/api/v0/searches/"+url.PathEscape(id)+"/responses", nil, &rs)
	return rs, err
}

// DeleteSearch removes a completed search from slskd.
func (c *Client) DeleteSearch(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v0/searches/"+url.PathEscape(id), nil, nil)
}

// ---- Enqueue ----

type EnqueueRequest struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type EnqueueResponse struct {
	// slskd <0.26: {"Enqueued": ["file", ...], "Failed": [...]}
	// slskd 0.26+: {"batch": {...}, "failures": [{"filename":..., "message":...}]}
	Enqueued []string `json:"enqueued"`
	Failed   []string `json:"failed"`
	Batch    map[string]any `json:"batch,omitempty"`
	Failures []struct {
		Filename string `json:"filename"`
		Message  string `json:"message"`
	} `json:"failures,omitempty"`
}

// Enqueue requests one or more files from a peer. Tolerates both slskd
// response shapes (0.25 string lists / 0.26 batch object) by decoding into a
// raw envelope; success = 2xx with no failures.
func (c *Client) Enqueue(ctx context.Context, username string, files []EnqueueRequest) (*EnqueueResponse, error) {
	var raw struct {
		Enqueued []any `json:"enqueued"`
		Failed   []any `json:"failed"`
		Batch    any   `json:"batch"`
		Failures []struct {
			Filename string `json:"filename"`
			Message  string `json:"message"`
		} `json:"failures"`
	}
	if err := c.do(ctx, http.MethodPost,
		"/api/v0/transfers/downloads/"+url.PathEscape(username), files, &raw); err != nil {
		return nil, err
	}
	out := &EnqueueResponse{}
	for _, f := range raw.Failures {
		out.Failed = append(out.Failed, f.Filename+": "+f.Message)
	}
	for _, e := range raw.Enqueued {
		if s, ok := e.(string); ok {
			out.Enqueued = append(out.Enqueued, s)
		}
	}
	if raw.Batch == nil && raw.Failures == nil {
		for _, f := range raw.Failed {
			if s, ok := f.(string); ok {
				out.Failed = append(out.Failed, s)
			}
		}
	}
	return out, nil
}

// ---- Transfers ----

// FlexTime tolerates slskd's inconsistent timestamps: some carry a Z suffix,
// some (notably requestedAt) are bare "2006-01-02T15:04:05.999999999".
type FlexTime struct{ time.Time }

func (t *FlexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if v, err := time.Parse(layout, s); err == nil {
			t.Time = v
			return nil
		}
	}
	return nil // unparseable timestamps are non-fatal; leave zero
}

// Transfer is one file transfer record (one per retry attempt).
type Transfer struct {
	ID               string     `json:"id"`
	Filename         string     `json:"filename"`
	Size             int64      `json:"size"`
	BytesTransferred int64      `json:"bytesTransferred"`
	PercentComplete  float64    `json:"percentComplete"`
	AverageSpeed     float64    `json:"averageSpeed"`
	State            string     `json:"state"` // comma-joined flags, e.g. "Completed, Succeeded"
	PlaceInQueue     *int       `json:"placeInQueue"`
	RequestedAt      *FlexTime  `json:"requestedAt"`
	StartedAt        *FlexTime  `json:"startedAt"`
}

// GetDownloads returns transfers grouped per user. Handles both slskd API
// shapes: the flat UserResponse array (0.26+) and the older per-user tree.
func (c *Client) GetDownloads(ctx context.Context) (map[string][]Transfer, error) {
	// slskd 0.26+: array of { username, directories: [{ directory, files }] }
	var flat []struct {
		Username    string `json:"username"`
		Directories []struct {
			Directory string     `json:"directory"`
			Files     []Transfer `json:"files"`
		} `json:"directories"`
	}
	flatErr := c.do(ctx, http.MethodGet, "/api/v0/transfers/downloads", nil, &flat)
	if flatErr == nil {
		out := make(map[string][]Transfer, len(flat))
		for _, u := range flat {
			var ts []Transfer
			for _, d := range u.Directories {
				ts = append(ts, d.Files...)
			}
			out[u.Username] = ts
		}
		return out, nil
	}

	// legacy (<= 0.25): per-user tree of { directories: [{ transfers }] }
	var tree map[string]struct {
		Directory []struct {
			Transfers []Transfer `json:"transfers"`
		} `json:"directories"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v0/transfers/downloads", nil, &tree); err != nil {
		return nil, fmt.Errorf("transfers: %v / %v", flatErr, err)
	}
	out := make(map[string][]Transfer, len(tree))
	for user, rec := range tree {
		var ts []Transfer
		for _, d := range rec.Directory {
			ts = append(ts, d.Transfers...)
		}
		out[user] = ts
	}
	return out, nil
}

// CancelTransfer removes a transfer (and optionally its queued file).
func (c *Client) CancelTransfer(ctx context.Context, username, transferID string, remove bool) error {
	path := fmt.Sprintf("/api/v0/transfers/downloads/%s/%s?remove=%t",
		url.PathEscape(username), url.PathEscape(transferID), remove)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
