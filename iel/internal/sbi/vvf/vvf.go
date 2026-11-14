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
	return &Client{mgmt: mgmt, http: httpClient}
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
		return fmt.Errorf("the config API of the process VVFs is not defined yet: %s on %s (use orchestrator.vvf: dryrun)", in.Op, in.Target)
	default:
		return fmt.Errorf("vvf does not handle %s", in.Op)
	}
}

func (c *Client) post(ctx context.Context, url string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(url, "/"), bytes.NewReader(b))
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
