// Package vvf is the VVF control client of the SFC Provisioner: the input a switcher VVF (select)
// sends on, and the function configs of process VVFs. No M-plane component manages the SR functions
// yet, so it provisionally treats the MGMT REST API of the routers the VVFs run on (the testbed
// router API, as D4S used it) as the M-plane; it moves to that component once one exists.
package vvf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Client sends VVF control instructions to the routers' MGMT REST API.
type Client struct {
	// mgmt maps TED node ids to the base URL of their router's MGMT REST API.
	mgmt map[string]string
	http *http.Client
}

// New returns a client for the given MGMT base URLs (TED node id -> e.g. http://10.0.0.1:8080).
// A nil httpClient uses http.DefaultClient (the provisioner bounds every request by its context).
func New(mgmt map[string]string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	bases := make(map[string]string, len(mgmt))
	for id, base := range mgmt {
		bases[id] = strings.TrimRight(base, "/")
	}
	return &Client{mgmt: bases, http: httpClient}
}

// Name implements provision.Driver.
func (*Client) Name() string { return "vvf" }

// Apply implements provision.Driver for switcher.select and function.set_params.
func (c *Client) Apply(ctx context.Context, in domain.Instruction) error {
	base, ok := c.mgmt[in.Target]
	if !ok {
		return fmt.Errorf("no MGMT endpoint for VVF node %s (orchestrator.nodes.<host>.mgmt)", in.Target)
	}
	switch in.Op {
	case domain.OpSwitcherSelect:
		if in.Select == nil {
			return fmt.Errorf("%s without an input", in.Op)
		}
		// next is the rank of the input SIDs: 0 sends the first input (swap off), 1.. swap to the
		// next ones.
		return c.post(ctx, base+"/config/stream_switch", map[string]any{"next": in.Select.Next, "enable": in.Select.Next > 0})
	case domain.OpFunctionSetParams:
		return c.configure(ctx, base, in)
	default:
		return fmt.Errorf("vvf does not handle %s", in.Op)
	}
}

// config is the router API call that sets one function of a process VVF: the path under the MGMT
// base URL and the body built from the function's parameters (catalog param_schema). An empty path
// means there is nothing to set.
type config struct {
	path string
	body func(params map[string]any) (map[string]any, error)
}

// configs maps function types to their config call. A type with no entry has a config API that is
// not defined yet.
var configs = map[string]config{
	"caption":   {}, // a fixed image for now
	"transcode": {}, // no parameters
	// crop cuts an FHD region out of the 8K stream: x, y is its top-left corner.
	"crop": {
		path: "/config/stream_generator_full_clipping",
		body: func(params map[string]any) (map[string]any, error) {
			x, err := intParam(params, "x")
			if err != nil {
				return nil, err
			}
			y, err := intParam(params, "y")
			if err != nil {
				return nil, err
			}
			return map[string]any{"name": "clip", "x": x, "y": y}, nil
		},
	},
	// color shifts the R, G, B levels of the stream.
	"color": {
		path: "/config/stream_converter_rgb",
		body: func(params map[string]any) (map[string]any, error) {
			body := map[string]any{}
			for _, c := range []string{"r", "g", "b"} {
				v, err := intParam(params, c)
				if err != nil {
					return nil, err
				}
				body[c] = v
			}
			return body, nil
		},
	},
}

// configure sends the config of every function of a process VVF node. It checks every function
// first, so that a node is never left half configured because of a function it cannot set.
func (c *Client) configure(ctx context.Context, base string, in domain.Instruction) error {
	type call struct {
		path string
		body map[string]any
	}
	var calls []call
	for _, f := range in.Functions {
		cfg, ok := configs[f.Type]
		if !ok {
			return fmt.Errorf("%s on %s: the config API of %s is not defined yet (use orchestrator.vvf: dryrun)", in.Op, in.Target, f.Type)
		}
		if cfg.path == "" {
			continue
		}
		body, err := cfg.body(f.Params)
		if err != nil {
			return fmt.Errorf("%s on %s: %s: %w", in.Op, in.Target, f.Type, err)
		}
		calls = append(calls, call{cfg.path, body})
	}
	for _, cl := range calls {
		if err := c.post(ctx, base+cl.path, cl.body); err != nil {
			return err
		}
	}
	return nil
}

// intParam reads an integer parameter (JSON numbers decode as float64).
func intParam(params map[string]any, name string) (int64, error) {
	switch v := params[name].(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("%s = %v is not an integer", name, v)
		}
		return int64(v), nil
	case json.Number:
		return v.Int64()
	case nil:
		return 0, fmt.Errorf("%s is missing", name)
	default:
		return 0, fmt.Errorf("%s = %v is not a number", name, v)
	}
}

func (c *Client) post(ctx context.Context, url string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("POST %s: %s %s", url, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Selected reads the input a switcher VVF sends on (Intent Assurance monitoring), as D4S reads the
// testbed routers: GET /show/stream_swap tells whether swap is on; off sends the first input (next
// 0). On, GET /show/stream_switch lists the stream threads: the active one's src "<x>_<letter>..."
// names the swapped-in stream, whose input is letter + 1 (src "_a" is next 1, the second input); no
// thread at all is next 1 too.
func (c *Client) Selected(ctx context.Context, nodeID string) (int, error) {
	base, ok := c.mgmt[nodeID]
	if !ok {
		return 0, fmt.Errorf("no MGMT endpoint for VVF node %s (orchestrator.nodes.<host>.mgmt)", nodeID)
	}
	var swap struct {
		Threads []struct {
			Enable bool `json:"enable"`
		} `json:"threads"`
	}
	if err := c.get(ctx, base+"/show/stream_swap", &swap); err != nil {
		return 0, err
	}
	if len(swap.Threads) == 0 {
		return 0, fmt.Errorf("%s: /show/stream_swap lists no thread", nodeID)
	}
	if !swap.Threads[0].Enable {
		return 0, nil
	}
	var sw struct {
		Threads [][]struct {
			Src      string `json:"src"`
			IsActive bool   `json:"is_active"`
		} `json:"threads"`
	}
	if err := c.get(ctx, base+"/show/stream_switch", &sw); err != nil {
		return 0, err
	}
	seen := false
	for _, group := range sw.Threads {
		for _, t := range group {
			seen = true
			if !t.IsActive {
				continue
			}
			_, id, ok := strings.Cut(t.Src, "_")
			if !ok || id == "" || id[0] < 'a' || id[0] > 'z' {
				return 0, fmt.Errorf("%s: unknown stream src %q", nodeID, t.Src)
			}
			return int(id[0]-'a') + 1, nil
		}
	}
	if !seen {
		return 1, nil
	}
	return 0, fmt.Errorf("%s: no active stream", nodeID)
}

func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s %s", url, resp.Status, strings.TrimSpace(string(msg)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	return nil
}
