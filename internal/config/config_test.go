package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestLoadExecutorRealFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
executor:
  mode: real
  ssh_user: root
  remote_scripts_dir: /export/packages/scripts
  merge_script: /export/op/hefu/merge.sh
  concurrency: 8
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Executor.SSHUser != "root" {
		t.Errorf("ssh_user = %q, want root", cfg.Executor.SSHUser)
	}
	if cfg.Executor.RemoteScriptsDir != "/export/packages/scripts" {
		t.Errorf("remote_scripts_dir = %q", cfg.Executor.RemoteScriptsDir)
	}
	if cfg.Executor.MergeScript != "/export/op/hefu/merge.sh" {
		t.Errorf("merge_script = %q", cfg.Executor.MergeScript)
	}
	if cfg.Executor.Concurrency != 8 {
		t.Errorf("concurrency = %d, want 8", cfg.Executor.Concurrency)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: 9090
database:
  driver: sqlite
  dsn: test.db
executor:
  mode: mock
auth:
  password_enabled: true
  wework_enabled: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("driver = %q, want sqlite", cfg.Database.Driver)
	}
	if cfg.Executor.Mode != "mock" {
		t.Errorf("mode = %q, want mock", cfg.Executor.Mode)
	}
	if !cfg.Auth.PasswordEnabled {
		t.Errorf("password_enabled = false, want true")
	}
}

func TestLoadHefuAndStopRetries(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("executor:\n  hefu_dir: /export/op/hefu\n  stop_retries: 2\n"), 0644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Executor.HefuDir != "/export/op/hefu" {
		t.Errorf("HefuDir=%q", c.Executor.HefuDir)
	}
	if c.Executor.StopRetries != 2 {
		t.Errorf("StopRetries=%d", c.Executor.StopRetries)
	}
}

func TestMergePreviewDSN(t *testing.T) {
	// 默认:复用 gsconfig.db_dsn,只换 database 为 mergepreview
	var c Config
	c.GSConfig.DBDSN = "test:test@tcp(127.0.0.1:3306)/gameconfig?charset=utf8mb4&parseTime=true"
	got, err := c.MergePreviewDSN()
	if err != nil {
		t.Fatalf("MergePreviewDSN error: %v", err)
	}
	if mc, _ := mysqlParse(got); mc != "mergepreview" {
		t.Errorf("derived db = %q, want mergepreview (dsn=%q)", mc, got)
	}
	if c.MergePreviewDatabase() != "mergepreview" {
		t.Errorf("MergePreviewDatabase = %q, want mergepreview", c.MergePreviewDatabase())
	}

	// 自定义库名
	c.MergePreview.Database = "mp_custom"
	got, _ = c.MergePreviewDSN()
	if mc, _ := mysqlParse(got); mc != "mp_custom" {
		t.Errorf("derived db = %q, want mp_custom", mc)
	}

	// 显式 db_dsn 覆盖优先
	c.MergePreview.DBDSN = "u:p@tcp(10.0.0.9:3306)/other?parseTime=true"
	got, _ = c.MergePreviewDSN()
	if got != c.MergePreview.DBDSN {
		t.Errorf("override not honored: %q", got)
	}
	if c.MergePreviewDatabase() != "other" {
		t.Errorf("MergePreviewDatabase override = %q, want other", c.MergePreviewDatabase())
	}

	// gsconfig 为空且无覆盖 → 空串(功能停用)
	var empty Config
	if got, _ := empty.MergePreviewDSN(); got != "" {
		t.Errorf("empty cfg DSN = %q, want empty", got)
	}
}

// mysqlParse 取 DSN 里的 database 名(测试辅助)。
func mysqlParse(dsn string) (string, error) {
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	return mc.DBName, nil
}

func TestLoadWorkflow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
workflow:
  auto_execute_after_approve: true
  auto_open_after_test: true
  scheduler_tick_s: 15
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !cfg.Workflow.AutoExecuteAfterApprove || !cfg.Workflow.AutoOpenAfterTest {
		t.Errorf("auto flags = %+v, want both true", cfg.Workflow)
	}
	if cfg.Workflow.SchedulerTickS != 15 {
		t.Errorf("scheduler_tick_s = %d, want 15", cfg.Workflow.SchedulerTickS)
	}
}
