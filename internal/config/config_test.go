package config

import (
	"testing"
	"time"
)

// Перевіряємо, що справжній config.yaml з кореня проєкту читається без помилок.
func TestLoadProjectConfig(t *testing.T) {
	// Прибираємо змінні оточення на час тесту, щоб вони не вплинули на результат.
	t.Setenv("DATABASE_URL", "")

	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Orchestrator.AgentTimeout != 45*time.Minute {
		t.Errorf("agent_timeout = %s, очікував 45m", cfg.Orchestrator.AgentTimeout)
	}
	if cfg.Orchestrator.MaxReviewCycles != 3 {
		t.Errorf("max_review_cycles = %d", cfg.Orchestrator.MaxReviewCycles)
	}
	if len(cfg.Roles["tester"].CLIFlags) != 3 {
		t.Errorf("tester cli_flags = %v", cfg.Roles["tester"].CLIFlags)
	}
	if len(cfg.Roles["jira"].CLIFlags) == 0 {
		t.Error("роль jira без cli_flags (потрібен --mcp-config з Jira MCP)")
	}
	if cfg.Orchestrator.MainRepo == "" {
		t.Error("main_repo не задано")
	}
	want := "postgres://orchestrator:orchestrator_secret_password@localhost:5432/orchestrator_db?sslmode=disable"
	if got := cfg.DatabaseURL(); got != want {
		t.Errorf("DatabaseURL() = %s", got)
	}
}

func TestEnvOverridesDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@y/z")
	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL() != "postgres://x@y/z" {
		t.Errorf("env не перевизначив DATABASE_URL: %s", cfg.DatabaseURL())
	}
}
