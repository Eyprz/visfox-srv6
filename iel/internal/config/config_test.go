package config

import (
	"log/slog"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("VISFOX_LISTEN", "")
	t.Setenv("VISFOX_LOG_LEVEL", "")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadFlagsOverrideEnv(t *testing.T) {
	t.Setenv("VISFOX_LISTEN", ":9000")
	t.Setenv("VISFOX_LOG_LEVEL", "warn")
	cfg, err := Load([]string{"-listen", ":7000"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":7000" || cfg.LogLevel != slog.LevelWarn {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsUnknownLevel(t *testing.T) {
	if _, err := Load([]string{"-log-level", "loud"}); err == nil {
		t.Fatal("expected an error for an unknown log level")
	}
}

func TestLoadRouterTimeout(t *testing.T) {
	cfg, err := Load([]string{"-router-timeout", "1500ms"})
	if err != nil || cfg.RouterTimeout.Milliseconds() != 1500 {
		t.Fatalf("timeout = %v, %v", cfg.RouterTimeout, err)
	}
	if _, err := Load([]string{"-router-timeout", "soon"}); err == nil {
		t.Fatal("expected an error for an invalid timeout")
	}
}

func TestLoadPCEReadyTimeout(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil || cfg.PCEReadyTimeout.Seconds() != 5 {
		t.Fatalf("default = %v, %v", cfg.PCEReadyTimeout, err)
	}
	t.Setenv("VISFOX_PCE_READY_TIMEOUT", "0")
	if cfg, err := Load(nil); err != nil || cfg.PCEReadyTimeout != 0 {
		t.Fatalf("0 = %v, %v", cfg.PCEReadyTimeout, err)
	}
	if _, err := Load([]string{"-pce-ready-timeout", "-1s"}); err == nil {
		t.Fatal("expected an error for a negative timeout")
	}
}

func TestLoadDrivers(t *testing.T) {
	t.Setenv("VISFOX_VVF", "mgmt")
	t.Setenv("VISFOX_DRY_RUN", "true")
	cfg, err := Load(nil)
	if err != nil || cfg.VVFDriver != "mgmt" || !cfg.DryRun {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	t.Setenv("VISFOX_DRY_RUN", "")
	if cfg, err = Load([]string{"-dry-run"}); err != nil || !cfg.DryRun {
		t.Fatalf("flag: cfg = %+v, %v", cfg, err)
	}
}
