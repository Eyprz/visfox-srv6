// Command visfox runs the VISFOX-SRv6 Intent-Enabled Layer.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	_ "modernc.org/sqlite"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/db"
	"github.com/eyprz/visfox-srv6/iel/internal/assurance/monitor"
	"github.com/eyprz/visfox-srv6/iel/internal/assurance/report"
	"github.com/eyprz/visfox-srv6/iel/internal/config"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ingestion/nbi"
	"github.com/eyprz/visfox-srv6/iel/internal/ingestion/usecase"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/provision"
	"github.com/eyprz/visfox-srv6/iel/internal/sbi/bgpls"
	"github.com/eyprz/visfox-srv6/iel/internal/sbi/dryrun"
	"github.com/eyprz/visfox-srv6/iel/internal/sbi/pce"
	"github.com/eyprz/visfox-srv6/iel/internal/sbi/vvf"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

func main() {
	if err := run(); err != nil {
		slog.Error("visfox stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	started := time.Now()
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}
	level := new(slog.LevelVar)
	level.Set(cfg.LogLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := openDB(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	op, err := config.LoadOperator(cfg.OperatorConfigPath)
	if err != nil {
		return err
	}
	if err := op.Override(cfg.SRPolicyDriver, cfg.VVFDriver, cfg.PCEPath, cfg.DryRun); err != nil {
		return err
	}
	var src ted.Source = ted.SeedFile{Path: cfg.TEDSeedPath}
	srcName := cfg.TEDSeedPath
	if op.TED.Source == config.TEDGoBGP {
		src = bgpls.Source{Target: op.TED.GoBGP}
		srcName = "gobgp " + op.TED.GoBGP
		logger.Info("reading the TED from BGP-LS", "gobgp", op.TED.GoBGP)
	}
	t, err := ted.Build(ctx, src, op.SIDRule, op.Bindings(), store)
	if err != nil {
		return fmt.Errorf("build TED: %w", err)
	}
	logger.Info("TED loaded", "source", srcName, "rule", op.SIDRule.Name, "nodes", len(t.Nodes),
		"sources", len(t.Sources), "destinations", len(t.Destinations), "unclassified", t.Unclassified)

	catalog, err := ssot.LoadCatalog(cfg.CatalogPath)
	if err != nil {
		return err
	}
	res, err := ssot.NewResources(t, catalog.Constraints.Vvfs, op.Wiring, op.Limits)
	if err != nil {
		return err
	}
	st := ssot.NewStore(store)
	seed, err := ssot.LoadPresetSeed(cfg.PresetsSeedPath)
	if err != nil {
		return err
	}
	// The seed presets go in on the first start only; afterwards presets are managed through the API.
	if seeded, err := st.SeedPresets(ctx, seed, time.Now().UTC()); err != nil {
		return fmt.Errorf("seed presets: %w", err)
	} else if seeded {
		logger.Info("presets seeded", "source", cfg.PresetsSeedPath, "presets", len(seed.Presets))
	}

	// A TED learned over BGP-LS is followed while the IEL runs (every assurance pass).
	var readTED func(context.Context) (*ssot.Resources, error)
	var saveTED func(context.Context, domain.TED) error
	if op.TED.Source == config.TEDGoBGP {
		// A pass runs every few seconds: read the RIB twice 0.5 s apart to see it is stable.
		follow := bgpls.Source{Target: op.TED.GoBGP, Wait: 10 * time.Second, Poll: 500 * time.Millisecond}
		readTED = func(ctx context.Context) (*ssot.Resources, error) {
			dev, err := follow.Load(ctx)
			if err != nil {
				return nil, err
			}
			t, err := ted.Interpret(dev, op.SIDRule, op.Bindings())
			if err != nil {
				return nil, err
			}
			return ssot.NewResources(t, catalog.Constraints.Vvfs, op.Wiring, op.Limits)
		}
		saveTED = func(ctx context.Context, t domain.TED) error { return ted.Save(ctx, store, t) }
	}

	prov, mon, switches, closeSBI, err := newProvisioner(ctx, op.Orchestrator, res, cfg.RouterTimeout, cfg.PCEReadyTimeout, logger)
	if err != nil {
		return err
	}
	defer closeSBI()

	events := report.NewBroker(logger)
	go events.RunHeartbeat(ctx)
	uc, err := usecase.New(ctx, usecase.Options{
		Store: st, Resources: res, Catalog: catalog, Publisher: events, Logger: logger,
		Provisioner: prov, Monitor: mon, Switches: switches, ReadTimeout: cfg.RouterTimeout,
		ReadTED: readTED, SaveTED: saveTED, Arg: op.SIDRule.Arg, ColorBase: op.Orchestrator.ColorBase,
		Immutable: apitypes.ImmutableConfig{
			ListenAddr: cfg.ListenAddr, DbPath: cfg.DBPath, RouterTimeoutMs: int(cfg.RouterTimeout.Milliseconds()),
			SeedPath: &srcName,
		},
		DefaultConfig: apitypes.DynamicConfig{
			SseIntervalMs: 1000, MetricsDumpIntervalS: 30, LogLevel: apitypes.LogLevel(strings.ToLower(cfg.LogLevel.String())),
			AssuranceIntervalS: 3, DriftAction: apitypes.DriftActionRepair,
		},
		OnLogLevel: func(l apitypes.LogLevel) {
			var lv slog.Level
			if err := lv.UnmarshalText([]byte(l)); err == nil {
				level.Set(lv)
			}
		},
	})
	if err != nil {
		return err
	}

	go assure(ctx, uc.AssureNetwork)

	e := nbi.New(logger, started, uc, events)
	logger.Info("starting IEL", "listen", cfg.ListenAddr)
	sc := echo.StartConfig{Address: cfg.ListenAddr, HideBanner: true, GracefulTimeout: 10 * time.Second}
	if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("IEL stopped")
	return nil
}

// openDB opens the SQLite database and applies the schema.
func openDB(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create DB directory: %w", err)
	}
	store, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Apply(ctx, store); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return store, nil
}

// newProvisioner builds the SFC Provisioner clients from the network operator config: SR Policies go
// to PolaPCE (PCE NBI client) and VVF control (the input selection of switchers, the configs of
// process VVFs) to the routers' MGMT REST API (VVF control client), or either is dry-run (logged).
// With PolaPCE the PCE client is also the monitor Intent Assurance reads the SR Policies from, with
// mgmt the VVF control client the one it reads the switcher inputs from (nil when dry-run).
func newProvisioner(ctx context.Context, oc config.Orchestrator, res *ssot.Resources, timeout, ready time.Duration, logger *slog.Logger) (*provision.Provisioner, monitor.SRPolicySource, monitor.SwitcherSource, func(), error) {
	dry := dryrun.Client{Log: func(m string) { logger.Info(m) }}
	drivers := map[domain.Op]provision.Driver{
		domain.OpSRPolicySet: dry, domain.OpSRPolicyDelete: dry,
		domain.OpSwitcherSelect: dry, domain.OpFunctionSetParams: dry,
	}
	byHost := map[string]string{}
	for _, n := range res.TED.Nodes {
		byHost[n.Hostname] = n.ID
	}
	nodeID := func(host string) (string, error) {
		id, ok := byHost[host]
		if !ok {
			return "", fmt.Errorf("orchestrator.nodes: %q is not in the TED", host)
		}
		return id, nil
	}
	closer := func() {}
	var mon monitor.SRPolicySource
	var switches monitor.SwitcherSource
	if oc.SRPolicy == config.DriverPolaPCE {
		addrs := map[string]pce.NodeAddress{}
		for host, a := range oc.Nodes {
			id, err := nodeID(host)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			var na pce.NodeAddress
			na.PCEPPeer, _ = netip.ParseAddr(a.PCEPPeer)
			na.Address, _ = netip.ParseAddr(a.Address)
			addrs[id] = na
		}
		opts := pce.Options{ASN: oc.PolaPCE.ASN, Validate: oc.PolaPCE.SIDValidate, Nodes: addrs,
			Dynamic: oc.PolaPCE.Path == config.PathDynamic}
		if opts.Dynamic {
			metric, err := pce.MetricType(oc.PolaPCE.Metric)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			// Waypoints name the router of every SID; Pola's TED knows the routers by router id.
			opts.Metric, opts.Routers = metric, map[string]string{}
			for _, n := range res.TED.Nodes {
				if n.RouterID == "" {
					continue
				}
				opts.Routers[n.ID] = n.RouterID
				for _, sid := range n.SIDs {
					opts.Routers[sid] = n.RouterID
				}
			}
		}
		pola, err := pce.New(oc.PolaPCE.Address, opts)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		drivers[domain.OpSRPolicySet], drivers[domain.OpSRPolicyDelete] = pola, pola
		closer = func() { _ = pola.Close() }
		mon = pola
		logger.Info("SR Policies go to PolaPCE", "address", oc.PolaPCE.Address, "path", oc.PolaPCE.Path)
		// The startup assurance pass repairs the network: wait until Pola can take SR Policies (a
		// Pola started with the IEL learns its PCEP sessions and TED in a few seconds). After the
		// wait the assurance passes repair what the startup pass could not.
		if ready > 0 {
			wait, cancel := context.WithTimeout(ctx, ready)
			err = pola.Wait(wait, 500*time.Millisecond)
			cancel()
			if err != nil {
				logger.Warn("PolaPCE not ready, starting anyway", "err", err, "waited", ready)
			} else {
				logger.Info("PolaPCE ready")
			}
		}
	} else {
		logger.Info("SR Policies are dry-run (logged)")
	}
	if oc.VVF == config.DriverMGMT {
		mgmt := map[string]string{}
		for host, a := range oc.Nodes {
			if a.MGMT == "" {
				continue
			}
			id, err := nodeID(host)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			mgmt[id] = a.MGMT
		}
		ctl := vvf.New(mgmt, nil)
		drivers[domain.OpSwitcherSelect], drivers[domain.OpFunctionSetParams] = ctl, ctl
		switches = ctl
		logger.Info("VVF control goes to the routers' MGMT REST API", "nodes", len(mgmt))
	} else {
		logger.Info("VVF control is dry-run (logged)")
	}
	return provision.New(provision.Options{
		Drivers: drivers, Timeout: timeout, Settle: time.Duration(oc.VVFSettleMs) * time.Millisecond,
		StreamSettle: time.Duration(oc.StreamSettleMs) * time.Millisecond,
	}), mon, switches, closer, nil
}

// assure runs Intent Assurance every assurance_interval_s (dynamic config). It checks the setting
// every second, so that a change from the GUI takes effect at once; 0 pauses it.
func assure(ctx context.Context, a usecase.AssureNetwork) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if every := a.Interval(); every > 0 && now.Sub(last) >= every {
				a.Run(ctx)
				last = time.Now()
			}
		}
	}
}
