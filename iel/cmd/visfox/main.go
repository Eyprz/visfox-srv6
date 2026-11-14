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

	op, err := config.LoadOperator(cfg.OperatorConfigPath, cfg.SRPolicyDriver)
	if err != nil {
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
	// Until intent translation (stage C) presets are predefined: the seed file is the source and
	// replaces the stored presets at every start; the API answers preset writes with 501.
	// Stage C sets presetEditing and seeds the first start only.
	const presetEditing = false
	if presetEditing {
		if seeded, err := st.SeedPresets(ctx, seed, time.Now().UTC()); err != nil {
			return fmt.Errorf("seed presets: %w", err)
		} else if seeded {
			logger.Info("presets seeded", "source", cfg.PresetsSeedPath, "presets", len(seed.Presets))
		}
	} else {
		removed, err := st.SyncPresets(ctx, seed, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("load predefined presets: %w", err)
		}
		logger.Info("predefined presets loaded", "source", cfg.PresetsSeedPath, "presets", len(seed.Presets),
			"removed", removed)
	}

	prov, mon, closeSBI, err := newProvisioner(op.Orchestrator, res, cfg.RouterTimeout, logger)
	if err != nil {
		return err
	}
	defer closeSBI()

	events := report.NewBroker(logger)
	go events.RunHeartbeat(ctx)
	uc, err := usecase.New(ctx, usecase.Options{
		Store: st, Resources: res, Catalog: catalog, Publisher: events, Logger: logger, PresetEditing: presetEditing,
		Provisioner: prov, Monitor: mon, Arg: op.SIDRule.Arg, ColorBase: op.Orchestrator.ColorBase,
		Immutable: apitypes.ImmutableConfig{
			ListenAddr: cfg.ListenAddr, DbPath: cfg.DBPath, RouterTimeoutMs: int(cfg.RouterTimeout.Milliseconds()),
			SeedPath: &srcName,
		},
		DefaultConfig: apitypes.DynamicConfig{
			SseIntervalMs: 1000, MetricsDumpIntervalS: 30, LogLevel: apitypes.LogLevel(strings.ToLower(cfg.LogLevel.String())),
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

// newProvisioner builds the SFC Provisioner clients from the network operator config: SR Policies
// go to PolaPCE (PCE NBI client) or are dry-run; the VVF nodes (switchers too) are dry-run until their VNF
// Manager client exists. With PolaPCE the PCE client is also the monitor Intent Assurance reads the
// SR Policies from (nil when dry-run).
func newProvisioner(oc config.Orchestrator, res *ssot.Resources, timeout time.Duration, logger *slog.Logger) (*provision.Provisioner, monitor.SRPolicySource, func(), error) {
	dry := dryrun.Client{Log: func(m string) { logger.Info(m) }}
	drivers := map[domain.Op]provision.Driver{
		domain.OpSRPolicySet: dry, domain.OpSRPolicyDelete: dry,
		domain.OpSwitcherSelect: dry, domain.OpFunctionSetParams: dry,
	}
	closer := func() {}
	var mon monitor.SRPolicySource
	if oc.SRPolicy == config.DriverPolaPCE {
		byHost := map[string]string{}
		for _, n := range res.TED.Nodes {
			byHost[n.Hostname] = n.ID
		}
		addrs := map[string]pce.NodeAddress{}
		for host, a := range oc.Nodes {
			id, ok := byHost[host]
			if !ok {
				return nil, nil, nil, fmt.Errorf("orchestrator.nodes: %q is not in the TED", host)
			}
			var na pce.NodeAddress
			na.PCEPPeer, _ = netip.ParseAddr(a.PCEPPeer)
			na.Address, _ = netip.ParseAddr(a.Address)
			addrs[id] = na
		}
		pola, err := pce.New(oc.PolaPCE.Address, oc.PolaPCE.ASN, oc.PolaPCE.SIDValidate, addrs)
		if err != nil {
			return nil, nil, nil, err
		}
		drivers[domain.OpSRPolicySet], drivers[domain.OpSRPolicyDelete] = pola, pola
		closer = func() { _ = pola.Close() }
		mon = pola
		logger.Info("SR Policies go to PolaPCE", "address", oc.PolaPCE.Address)
	} else {
		logger.Info("SR Policies are dry-run (orchestrator.sr_policy: dryrun)")
	}
	return provision.New(provision.Options{
		Drivers: drivers, Timeout: timeout, Settle: time.Duration(oc.VVFSettleMs) * time.Millisecond,
	}), mon, closer, nil
}
