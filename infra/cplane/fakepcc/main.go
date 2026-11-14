// Command fakepcc is a stand-in SRv6 headend for the local C-Plane lab (infra/cplane): a PCEP PCC
// (RFC 5440 / 8231 / 8281 / 9603) that opens a session to PolaPCE, accepts the SR Policies Pola
// initiates or updates, reports them back as active, and shows what it holds over HTTP
// (GET /policies). It forwards no packets. It is a test tool for VISFOX-SRv6, not part of the IEL.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/nttcom/pola/pkg/packet/pcep"
)

const headerLen = 4

// Policy is an SR Policy the headend holds.
type Policy struct {
	Name     string    `json:"name"`
	PlspID   uint32    `json:"plsp_id"`
	Color    uint32    `json:"color"`
	Endpoint string    `json:"endpoint"`
	Segments []string  `json:"segment_list"`
	Updated  time.Time `json:"updated_at"`
}

type pcc struct {
	name string
	log  *slog.Logger

	mu       sync.Mutex
	conn     net.Conn
	policies map[uint32]*Policy
	nextPlsp uint32
	up       bool
}

func main() {
	pce := env("FAKEPCC_PCE", "172.31.77.2:4189")
	listen := env("FAKEPCC_HTTP", ":8080")
	p := &pcc{name: env("FAKEPCC_NAME", "headend"), policies: map[uint32]*Policy{}, nextPlsp: 1,
		log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	http.HandleFunc("GET /policies", p.serveList)
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if !p.up {
			http.Error(w, "PCEP session down", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	go func() {
		srv := &http.Server{Addr: listen, ReadHeaderTimeout: 5 * time.Second}
		if err := srv.ListenAndServe(); err != nil {
			p.log.Error("http", "error", err)
			os.Exit(1)
		}
	}()
	for {
		if err := p.session(pce); err != nil {
			p.log.Warn("PCEP session ended", "pce", pce, "error", err)
		}
		p.mu.Lock()
		p.up = false
		p.mu.Unlock()
		time.Sleep(3 * time.Second)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (p *pcc) serveList(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	list := make([]Policy, 0, len(p.policies))
	for _, pol := range p.policies {
		list = append(list, *pol)
	}
	p.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].PlspID < list[j].PlspID })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// session runs one PCEP session: OPEN exchange, end of LSP-DB sync, then requests and keepalives.
func (p *pcc) session(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()

	caps := []pcep.CapabilityInterface{
		&pcep.StatefulPCECapability{LSPUpdateCapability: true, LSPInstantiationCapability: true, ColorCapability: true},
		&pcep.PathSetupTypeCapability{
			PathSetupTypes: pcep.Psts{pcep.PathSetupTypeSRTE, pcep.PathSetupTypeSRv6TE},
			SubTLVs:        []pcep.TLVInterface{&pcep.SRPCECapability{HasUnlimitedMaxSIDDepth: true}, &pcep.SRv6PCECapability{}},
		},
		&pcep.AssocTypeList{AssocTypes: []pcep.AssocType{pcep.AssocTypeSRPolicyAssociation}},
	}
	if err := p.send(pcep.NewOpenMessage(1, 30, 120, caps)); err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = p.send(pcep.NewKeepaliveMessage())
			}
		}
	}()

	for {
		var h pcep.CommonHeader
		head := make([]byte, headerLen)
		if _, err := io.ReadFull(conn, head); err != nil {
			return err
		}
		if err := h.DecodeFromBytes(head); err != nil {
			return err
		}
		body := make([]byte, int(h.MessageLength)-headerLen)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		switch h.MessageType {
		case pcep.MessageTypeOpen:
			if err := p.send(pcep.NewKeepaliveMessage()); err != nil {
				return err
			}
		case pcep.MessageTypeKeepalive:
			p.mu.Lock()
			first := !p.up
			p.up = true
			p.mu.Unlock()
			if first {
				p.log.Info("PCEP session up", "pce", addr, "pcc", p.name)
				// End of LSP-DB synchronization: an LSP object with PLSP-ID 0 (RFC 8231 §5.6).
				if err := p.sendReport(&pcep.SrpObject{ObjectType: pcep.ObjectTypeSRPSRP},
					&pcep.LSPObject{ObjectType: pcep.ObjectTypeLSPLSP}, &pcep.EroObject{ObjectType: pcep.ObjectTypeEROExplicitRoute}, nil); err != nil {
					return err
				}
			}
		case pcep.MessageTypeLSPInitReq, pcep.MessageTypeUpdate:
			if err := p.handleRequest(h.MessageType, body); err != nil {
				p.log.Error("request", "type", h.MessageType, "error", err)
			}
		case pcep.MessageTypeClose:
			return errors.New("PCE closed the session")
		default:
			p.log.Info("ignored message", "type", h.MessageType)
		}
	}
}

// handleRequest applies a PCInitiate / PCUpd and reports the result. Both carry the same objects
// as a state report (SRP, LSP, ERO, ASSOCIATION), so the PCRpt decoder reads them.
func (p *pcc) handleRequest(typ pcep.MessageType, body []byte) error {
	var m pcep.PCRptMessage
	if err := m.DecodeFromBytes(body); err != nil {
		return fmt.Errorf("decode %s: %w", typ, err)
	}
	for _, r := range m.StateReports {
		if err := p.apply(r); err != nil {
			return err
		}
	}
	return nil
}

func (p *pcc) apply(r *pcep.StateReport) error {
	srp, lsp := r.SrpObject, r.LSPObject
	remove := srp != nil && srp.RFlag
	var assoc *pcep.AssociationObject
	if len(r.AssociationObjects) > 0 {
		assoc = r.AssociationObjects[0]
	}

	p.mu.Lock()
	plsp := lsp.PlspID
	pol, known := p.policies[plsp]
	if !known && lsp.Name != "" {
		for id, x := range p.policies {
			if x.Name == lsp.Name {
				pol, plsp, known = x, id, true
			}
		}
	}
	switch {
	case remove:
		delete(p.policies, plsp)
	case !known:
		plsp = p.nextPlsp
		p.nextPlsp++
		pol = &Policy{PlspID: plsp}
		p.policies[plsp] = pol
	}
	if !remove {
		if lsp.Name != "" {
			pol.Name = lsp.Name
		}
		if c := lsp.Color(); c != 0 {
			pol.Color = c
		}
		if assoc != nil {
			if c := assoc.Color(); c != 0 {
				pol.Color = c
			}
			if e := assoc.Endpoint(); e.IsValid() {
				pol.Endpoint = e.String()
			}
		}
		if r.EroObject != nil {
			pol.Segments = pol.Segments[:0]
			for _, s := range r.EroObject.ToSegmentList() {
				pol.Segments = append(pol.Segments, s.SidString())
			}
		}
		pol.Updated = time.Now()
	}
	name := lsp.Name
	if pol != nil && pol.Name != "" {
		name = pol.Name
	}
	p.mu.Unlock()
	p.log.Info("SR Policy", "removed", remove, "name", name, "plsp", plsp, "policy", pol)

	color := lsp.Color()
	if pol != nil && pol.Color != 0 {
		color = pol.Color
	}
	out := pcep.NewLSPObject(name, &color, plsp)
	out.OFlag = 2 // ACTIVE
	out.RFlag = remove
	ero := r.EroObject
	if ero == nil {
		ero = &pcep.EroObject{ObjectType: pcep.ObjectTypeEROExplicitRoute}
	}
	if srp == nil {
		srp = &pcep.SrpObject{ObjectType: pcep.ObjectTypeSRPSRP}
	}
	return p.sendReport(srp, out, ero, assoc)
}

type serializer interface{ Serialize() ([]uint8, error) }

func (p *pcc) send(m serializer) error {
	b, err := m.Serialize()
	if err != nil {
		return err
	}
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	_, err = conn.Write(b)
	return err
}

// sendReport sends a PCRpt with one state report (Pola only decodes PCRpt, so it is built here).
func (p *pcc) sendReport(srp *pcep.SrpObject, lsp *pcep.LSPObject, ero *pcep.EroObject, assoc *pcep.AssociationObject) error {
	var body []byte
	for _, o := range []serializer{srp, lsp, ero} {
		b, err := o.Serialize()
		if err != nil {
			return err
		}
		body = append(body, b...)
	}
	if assoc != nil {
		b, err := assoc.Serialize()
		if err != nil {
			return err
		}
		body = append(body, b...)
	}
	head := make([]byte, headerLen)
	head[0] = 0x20 // version 1
	head[1] = byte(pcep.MessageTypeReport)
	binary.BigEndian.PutUint16(head[2:], uint16(headerLen+len(body)))
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	_, err := conn.Write(append(head, body...))
	return err
}
