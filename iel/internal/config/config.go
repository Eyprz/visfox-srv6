// Package config loads the IEL settings.
//
// Critical settings (listen address, DB path, timeouts) are fixed at startup from CLI flags or
// environment variables and never change at runtime.
package config

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Immutable holds the settings fixed at startup.
type Immutable struct {
	// ListenAddr is the address the REST / SSE server listens on (e.g. ":8080").
	ListenAddr string
	// LogLevel is the initial log level (debug, info, warn, error).
	LogLevel slog.Level
	// DBPath is the SQLite database file.
	DBPath string
	// TEDSeedPath is the static TED source (stand-in for BGP-LS).
	TEDSeedPath string
	// OperatorConfigPath is the network operator config (SID rule, IN / OUT bindings, wiring, limits).
	OperatorConfigPath string
	// CatalogPath is the VVF catalog and domain policy.
	CatalogPath string
	// PresetsSeedPath holds the presets inserted on the first start.
	PresetsSeedPath string
	// RouterTimeout bounds every southbound request (orchestrator drivers).
	RouterTimeout time.Duration
	// SRPolicyDriver overrides orchestrator.sr_policy of the network operator config ("dryrun" /
	// "polapce"); empty keeps the file's.
	SRPolicyDriver string
}

// Load reads the settings from CLI flags, falling back to VISFOX_* environment variables and defaults.
func Load(args []string) (Immutable, error) {
	fs := flag.NewFlagSet("visfox", flag.ContinueOnError)
	listen := fs.String("listen", env("VISFOX_LISTEN", ":8080"), "REST / SSE listen address (env VISFOX_LISTEN)")
	level := fs.String("log-level", env("VISFOX_LOG_LEVEL", "info"), "log level: debug, info, warn, error (env VISFOX_LOG_LEVEL)")
	dbPath := fs.String("db", env("VISFOX_DB_PATH", "data/visfox.db"), "SQLite database file (env VISFOX_DB_PATH)")
	seed := fs.String("ted-seed", env("VISFOX_TED_SEED", "db/ted.yaml"), "static TED source (env VISFOX_TED_SEED)")
	operator := fs.String("operator-config", env("VISFOX_OPERATOR_CONFIG", "config/operator.yaml"),
		"network operator config (env VISFOX_OPERATOR_CONFIG)")
	catalog := fs.String("catalog", env("VISFOX_CATALOG", "db/catalog.yaml"), "VVF catalog and domain policy (env VISFOX_CATALOG)")
	presets := fs.String("presets-seed", env("VISFOX_PRESETS_SEED", "db/presets.yaml"),
		"presets inserted on the first start (env VISFOX_PRESETS_SEED)")
	timeout := fs.String("router-timeout", env("VISFOX_ROUTER_TIMEOUT", "3s"), "southbound request timeout (env VISFOX_ROUTER_TIMEOUT)")
	srPolicy := fs.String("sr-policy", env("VISFOX_SR_POLICY", ""),
		"SR Policy driver, dryrun or polapce; overrides the network operator config (env VISFOX_SR_POLICY)")
	if err := fs.Parse(args); err != nil {
		return Immutable{}, err
	}

	var lv slog.Level
	if err := lv.UnmarshalText([]byte(strings.ToUpper(*level))); err != nil {
		return Immutable{}, fmt.Errorf("invalid log level %q: %w", *level, err)
	}
	rt, err := time.ParseDuration(*timeout)
	if err != nil || rt <= 0 {
		return Immutable{}, fmt.Errorf("invalid router timeout %q", *timeout)
	}
	return Immutable{
		ListenAddr:         *listen,
		LogLevel:           lv,
		DBPath:             *dbPath,
		TEDSeedPath:        *seed,
		OperatorConfigPath: *operator,
		CatalogPath:        *catalog,
		PresetsSeedPath:    *presets,
		RouterTimeout:      rt,
		SRPolicyDriver:     *srPolicy,
	}, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
