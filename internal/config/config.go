package config

import (
	"fmt"
	"os"

	"github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
)

// Config maps config.yaml.
type Config struct {
	Server struct {
		Port          int    `yaml:"port"`
		SessionSecret string `yaml:"session_secret"`
	} `yaml:"server"`
	Database struct {
		Driver string `yaml:"driver"`
		DSN    string `yaml:"dsn"`
	} `yaml:"database"`
	Executor struct {
		Mode               string `yaml:"mode"`
		SSHKey             string `yaml:"ssh_key"`
		SSHPort            int    `yaml:"ssh_port"`
		SSHUser            string `yaml:"ssh_user"`
		PackagesDir        string `yaml:"packages_dir"`
		RemoteScriptsDir   string `yaml:"remote_scripts_dir"`
		MergeScript        string `yaml:"merge_script"`
		ServerConfigFile   string `yaml:"server_config_file"`
		ShowAllServers     bool   `yaml:"show_all_servers"`
		Concurrency        int    `yaml:"concurrency"`
		SwapConcurrency    int    `yaml:"swap_concurrency"`
		BatchSize          int    `yaml:"batch_size"`
		LaunchDelayMs      int    `yaml:"launch_delay_ms"`
		PerHostConcurrency int    `yaml:"per_host_concurrency"`
		SSHRetries         int    `yaml:"ssh_retries"`
		SSHRetryBackoffMs  int    `yaml:"ssh_retry_backoff_ms"`
		SSHMultiplex       bool   `yaml:"ssh_multiplex"`
		StatusPollS        int    `yaml:"status_poll_s"`
		StatusTimeoutS     int    `yaml:"status_timeout_s"`
		HefuDir            string `yaml:"hefu_dir"`
		StopRetries        int    `yaml:"stop_retries"`
		HotRetryBackoffsS  []int  `yaml:"hot_retry_backoffs_s"`
		GMHotUpdateScript  string `yaml:"gm_hot_update_script"`
	} `yaml:"executor"`
	Auth struct {
		PasswordEnabled bool `yaml:"password_enabled"`
		WeworkEnabled   bool `yaml:"wework_enabled"`
	} `yaml:"auth"`
	Notify struct {
		Wework struct {
			Enabled bool   `yaml:"enabled"`
			Webhook string `yaml:"webhook"`
			BaseURL string `yaml:"base_url"`
		} `yaml:"wework"`
	} `yaml:"notify"`
	Workflow struct {
		AutoExecuteAfterApprove bool `yaml:"auto_execute_after_approve"`
		AutoOpenAfterTest       bool `yaml:"auto_open_after_test"`
		SchedulerTickS          int  `yaml:"scheduler_tick_s"`
	} `yaml:"workflow"`
	GSConfig struct {
		DBDSN           string `yaml:"db_dsn"`
		ServerDebugPath string `yaml:"server_debug_path"`
		ClientDebugPath string `yaml:"client_debug_path"`
		COS             struct {
			Region                string `yaml:"region"`
			Bucket                string `yaml:"bucket"`
			SecretID              string `yaml:"secret_id"`
			SecretKey             string `yaml:"secret_key"`
			ServerObjectKey       string `yaml:"server_object_key"`
			ClientObjectKey       string `yaml:"client_object_key"`
			MergePreviewObjectKey string `yaml:"merge_preview_object_key"`
		} `yaml:"cos"`
	} `yaml:"gsconfig"`
	ALB struct {
		Enabled             bool   `yaml:"enabled"`
		Region              string `yaml:"region"`
		ResourceGroupID     string `yaml:"resource_group_id"`
		VpcID               string `yaml:"vpc_id"`
		HealthCheckTemplate string `yaml:"health_check_template_id"`
		LoginLoadBalancerID string `yaml:"login_load_balancer_id"`
		LoginListenerID     string `yaml:"login_listener_id"`
		PayLoadBalancerID   string `yaml:"pay_load_balancer_id"`
		PayListenerID       string `yaml:"pay_listener_id"`
		WSLoadBalancerID    string `yaml:"ws_load_balancer_id"`
		WSListenerID        string `yaml:"ws_listener_id"`
	} `yaml:"alb"`
	MergePreview struct {
		Database string `yaml:"database"`
		DBDSN    string `yaml:"db_dsn"`
	} `yaml:"mergepreview"`
}

// MergePreviewDSN returns merge-preview DSN.
func (c *Config) MergePreviewDSN() (string, error) {
	if c.MergePreview.DBDSN != "" {
		return c.MergePreview.DBDSN, nil
	}
	if c.GSConfig.DBDSN == "" {
		return "", nil
	}
	mc, err := mysql.ParseDSN(c.GSConfig.DBDSN)
	if err != nil {
		return "", fmt.Errorf("parse gsconfig.db_dsn: %w", err)
	}
	dbName := c.MergePreview.Database
	if dbName == "" {
		dbName = "mergepreview"
	}
	mc.DBName = dbName
	return mc.FormatDSN(), nil
}

// MergePreviewDatabase returns merge-preview database name.
func (c *Config) MergePreviewDatabase() string {
	if c.MergePreview.DBDSN != "" {
		if mc, err := mysql.ParseDSN(c.MergePreview.DBDSN); err == nil {
			return mc.DBName
		}
		return ""
	}
	if c.MergePreview.Database != "" {
		return c.MergePreview.Database
	}
	return "mergepreview"
}

// Load reads YAML config from file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
