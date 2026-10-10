package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"campusconnect/internal/netguard"
)

// BaseURL is the production portal origin.
const BaseURL = "https://nap.ntvu.edu.cn"

const maxBody = 1 << 20

// Client calls the ePortal endpoints relative to a fixed origin.
type Client struct {
	http *netguard.Client
	base *url.URL
}

// NewClient returns a portal client. baseURL is scheme://host only.
func NewClient(httpClient *netguard.Client, baseURL *url.URL) *Client {
	b := *baseURL
	b.Path, b.RawPath, b.RawQuery, b.Fragment = "", "", "", ""
	return &Client{http: httpClient, base: &b}
}

func (c *Client) endpoint(path string, query url.Values) *url.URL {
	u := *c.base
	u.Path = path
	u.RawQuery = query.Encode()
	return &u
}

type envelope struct {
	Code    *int            `json:"code"`
	Message *string         `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) send(ctx context.Context, op string, req *http.Request) ([]byte, int, error) {
	resp, err := c.http.Do(req.WithContext(ctx))
	if err != nil {
		return nil, 0, &Error{Kind: KindUnavailable, Op: op, Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, resp.StatusCode, &Error{Kind: KindUnavailable, Op: op, Err: err}
	}
	return body, resp.StatusCode, nil
}

// call sends one JSON API request and decodes data into out. body nil means GET.
func (c *Client) call(ctx context.Context, op, path string, query url.Values, body any, out any) error {
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		method = http.MethodPost
		b, err := json.Marshal(body)
		if err != nil {
			return &Error{Kind: KindChanged, Op: op, Err: err}
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.endpoint(path, query).String(), reader)
	if err != nil {
		return &Error{Kind: KindChanged, Op: op, Err: err}
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if body != nil {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	raw, status, err := c.send(ctx, op, req)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return statusError(op, status)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &Error{Kind: KindChanged, Op: op, Message: "response is not JSON", Err: err}
	}
	if env.Code == nil {
		return &Error{Kind: KindChanged, Op: op, Message: "response has no code"}
	}
	if *env.Code != 200 {
		return &Error{Kind: KindUnavailable, Op: op, Code: *env.Code, Message: deref(env.Message)}
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return &Error{Kind: KindChanged, Op: op, Message: "unexpected data shape", Err: err}
		}
	}
	return nil
}

func statusError(op string, status int) error {
	kind := KindChanged
	if status >= 500 {
		kind = KindUnavailable
	}
	return &Error{Kind: kind, Op: op, Message: fmt.Sprintf("HTTP %d", status)}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
