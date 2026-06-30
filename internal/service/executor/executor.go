package executor

import (
	"time"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// LogFunc 执行器用来实时输出一行日志。
// 注意:执行器可能从多个 goroutine 并发调用本函数,实现方必须保证并发安全。
type LogFunc func(line string)

// Executor 执行一个工单的运维动作。
type Executor interface {
	Execute(wo *model.WorkOrder, log LogFunc) error
}

// OpenRunner 实现开放阶段(放开登录 + 恢复状态)。发版/合服执行器实现。
type OpenRunner interface {
	Open(wo *model.WorkOrder, log LogFunc) error
}

// ConfigStore 配置库读写(由 gsconfig.Store 实现)。合服工单改库用。
type ConfigStore interface {
	GetServer(id string) (map[string]string, error)
	UpdateServer(id string, fields map[string]string) error
}

// ConfigPublisher 配置生成/发布(由 gsconfig.Service 实现)。
type ConfigPublisher interface {
	GenerateAndPublish() error
	GenerateServerFile(path string) error
}

// ServerCreator 配置库读写(由 gsconfig.Store 实现),创建游戏服执行器写新行用。
type ServerCreator interface {
	AllRows() ([]map[string]string, error)
	AddServers(rows []map[string]string) error
}

// RealConfig 真实执行器所需配置(计划二使用)。
type RealConfig struct {
	SSHKey             string
	SSHPort            int
	SSHUser            string
	PackagesDir        string
	RemoteScriptsDir   string
	MergeScript        string
	ServerConfigFile   string
	Source             gameserver.Source // 服务器数据来源(配置库);release/hotupdate 用。merge 仍用 ServerConfigFile
	Concurrency        int               // 全局并发上限(默认按配置)
	SwapConcurrency    int               // 换包并发上限(默认8;吃EFS带宽,单独限流)
	BatchSize          int               // 分批执行,每批服数量(默认50)
	LaunchDelay        time.Duration     // 每台启动SSH前的延时(默认400ms,防瞬时并发被拒)
	PerHostConcurrency int               // 同一台机器最多同时几个SSH(默认3)
	SSHRetries         int               // 连接类失败的最大尝试次数(默认3)
	SSHRetryBackoff    time.Duration     // 重试基准退避(默认500ms,指数+抖动)
	SSHMultiplex       bool              // 开启SSH连接复用(ControlMaster),同主机多次调用共用一条通道
	StatusPollInterval time.Duration     // 开服后轮询 status 的间隔(默认3s)
	StatusTimeout      time.Duration     // 等待 status==7 的最长时间(默认120s)
	HefuDir            string            // 设维护脚本目录
	StopRetries        int               // 停服失败重试次数(默认2)
	HotRetryBackoffs   []time.Duration   // 热更失败主机分波重试的波间隔(默认 [2s,3s,5s])
	GMHotUpdateScript  string            // GM热更脚本本地路径(默认 /op/scripts/gm_hot_update.sh)
	ConfigStore        ConfigStore       // 配置库读写(合服改库用)
	ConfigPublisher    ConfigPublisher   // 配置生成+COS发布 + 生成 server 文件
	COS                COSClient         // 配置类工单上传/下载/基准校验
	Artifacts          ArtifactStore     // 配置类工单工件读取与版本回填
	Creator            ServerCreator     // 创建游戏服:写新配置行
	Deleter            ServerDeleter     // 删除游戏服:配置库删行
	ALB                *ALBConfig        // 阿里云 ALB(SDK 创建/拆除转发规则);nil 或 Enabled=false 时跳过
}

// Dispatch 按模式与类型返回执行器。
func Dispatch(mode, woType string, cfg *RealConfig) Executor {
	if mode == "real" {
		switch woType {
		case model.TypeMerge:
			return NewMergeExecutor(cfg)
		case model.TypeRelease:
			return NewReleaseExecutor(cfg)
		case model.TypeHotupdate:
			return NewHotUpdateExecutor(cfg)
		case model.TypeConfigPush:
			return NewConfigPushExecutor(cfg)
		case model.TypeMergePublish:
			return NewMergePublishExecutor(cfg)
		case model.TypeNewServer:
			return NewNewServerExecutor(cfg)
		case model.TypeDeleteServer:
			return NewDeleteServerExecutor(cfg)
		}
	}
	return NewMock()
}
