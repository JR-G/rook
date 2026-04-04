package config

import "testing"

func TestAutonomyDefaultsAndValidation(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if !cfg.Autonomy.ObserveAgentChannels {
		t.Fatal("expected ambient observation to default on")
	}
	if cfg.Autonomy.WeeknotePostTime != "10:00" {
		t.Fatalf("unexpected default weeknote time %q", cfg.Autonomy.WeeknotePostTime)
	}
	if cfg.Autonomy.PollInterval.String() != "1m0s" && cfg.Autonomy.PollInterval.String() != "1m" {
		t.Fatalf("unexpected default autonomy poll interval %q", cfg.Autonomy.PollInterval)
	}

	cfg.Autonomy.WeeknotesEnabled = true
	if err := validate(cfg); err == nil {
		t.Fatal("expected missing weeknote channel to fail validation")
	}

	cfg.Autonomy.WeeknotesChannel = "C-WEEK"
	cfg.Autonomy.WeeknotePostTime = "25:00"
	if err := validate(cfg); err == nil {
		t.Fatal("expected invalid weeknote time to fail validation")
	}

	cfg.Autonomy.WeeknotePostTime = "09:30"
	if err := validate(cfg); err != nil {
		t.Fatalf("expected valid autonomy config, got %v", err)
	}
}

func TestParseClockHHMMAndEnvOverrides(t *testing.T) {
	hour, minute, err := ParseClockHHMM("14:05")
	if err != nil {
		t.Fatalf("ParseClockHHMM: %v", err)
	}
	if hour != 14 || minute != 5 {
		t.Fatalf("unexpected parsed clock %d:%d", hour, minute)
	}

	if _, _, err := ParseClockHHMM("bad"); err == nil {
		t.Fatal("expected invalid clock parse to fail")
	}

	cfg := Default()
	t.Setenv("ROOK_AUTONOMY_WEEKNOTES_CHANNEL", "C-WEEK")
	t.Setenv("ROOK_AUTONOMY_WEEKNOTE_POST_TIME", "11:45")
	applyEnv(&cfg)
	if cfg.Autonomy.WeeknotesChannel != "C-WEEK" {
		t.Fatalf("unexpected env weeknotes channel %q", cfg.Autonomy.WeeknotesChannel)
	}
	if cfg.Autonomy.WeeknotePostTime != "11:45" {
		t.Fatalf("unexpected env weeknote post time %q", cfg.Autonomy.WeeknotePostTime)
	}
}
