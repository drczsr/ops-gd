package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MergePair 合服一组:目标服 <- 源服。
type MergePair struct {
	Target int `json:"target"`
	Source int `json:"source"`
}

// MergeParams 合服工单参数(合服 / 预合服 两勾选项,可同勾)。
type MergeParams struct {
	IncludeMerge    bool        `json:"include_merge"`            // 勾"合服"
	IncludePreMerge bool        `json:"include_premerge"`         // 勾"预合服"
	Pairs           []MergePair `json:"pairs,omitempty"`          // 合服列表(IncludeMerge 必填)
	ToolPackage     string      `json:"tool_package,omitempty"`   // 合服工具包(IncludeMerge 必填)
	PreMergeIDs     []int       `json:"premerge_ids,omitempty"`   // 预合服服号(IncludePreMerge 必填)
	PreMergeDate    string      `json:"premerge_date,omitempty"`  // 预合服时间 YYYYMMDD
	ConfigPackage   string      `json:"config_package,omitempty"` // 已废弃:新流程不用 config 包(留字段保兼容)
}

// ReleaseParams 发版工单参数。
type ReleaseParams struct {
	ServerIDs        []int    `json:"server_ids"`
	VersionPackage   string   `json:"version_package"`
	IncludeBattle    bool     `json:"include_battle"`              // 是否连带处理战斗服+副本服
	LastFailedIDs    []int    `json:"last_failed_ids,omitempty"`   // 上次执行失败的具体服ID;重试时只发这些
	IncludeHotUpdate bool     `json:"include_hotupdate,omitempty"` // 发版成功后热更全服
	HotScope         string   `json:"hot_scope,omitempty"`         // 热更范围:selected=当前选中服 / all=全服;勾热更时必填
	HotPackage       string   `json:"hot_package,omitempty"`       // 热更配置包
	HotFiles         string   `json:"hot_files,omitempty"`         // 热更文件列表(逗号分隔)
	HotFailedHosts   []string `json:"hot_failed_hosts,omitempty"`  // 上次热更失败主机IP;重试只跑这些
	ConfigFailedIDs  []int    `json:"config_failed_ids,omitempty"` // 上次配置更新失败的服ID;重试只补跑这些
}

// HotupdateParams 热更工单参数。
type HotupdateParams struct {
	ServerIDs     []int  `json:"server_ids"`
	ConfigPackage string `json:"config_package"`
	HotFiles      string `json:"hot_files"`
	IncludeBattle bool   `json:"include_battle"`            // 是否连带处理战斗服+副本服
	LastFailedIDs []int  `json:"last_failed_ids,omitempty"` // 上次执行失败的具体服ID;重试时只发这些
}

// Summary 实现:目标概要(列表页展示用)。
func (p MergeParams) Summary() string {
	var parts []string
	if p.IncludeMerge {
		parts = append(parts, fmt.Sprintf("合服%d组", len(p.Pairs)))
	}
	if p.IncludePreMerge {
		parts = append(parts, fmt.Sprintf("预合服%d个", len(p.PreMergeIDs)))
	}
	return strings.Join(parts, " + ")
}
func (p ReleaseParams) Summary() string   { return joinInts(p.ServerIDs) }

// Validate 创建工单时的必填校验:不合法不该建单(而非拖到执行时才报)。
func (p ReleaseParams) Validate() error {
	if len(p.ServerIDs) == 0 {
		return fmt.Errorf("请至少选择一个服务器")
	}
	if strings.TrimSpace(p.VersionPackage) == "" {
		return fmt.Errorf("请填写版本包")
	}
	if p.IncludeHotUpdate {
		if p.HotScope != "selected" && p.HotScope != "all" {
			return fmt.Errorf("勾选发版后热更时,必须选择热更范围(当前选中服/全服)")
		}
		if strings.TrimSpace(p.HotPackage) == "" {
			return fmt.Errorf("勾选发版后热更时,必须填写热更包")
		}
		if strings.TrimSpace(p.HotFiles) == "" {
			return fmt.Errorf("勾选发版后热更时,必须填写热更文件列表")
		}
	}
	return nil
}
func (p HotupdateParams) Summary() string { return joinInts(p.ServerIDs) }

// Validate 创建热更工单时的必填校验:不合法不该建单。
func (p HotupdateParams) Validate() error {
	if len(p.ServerIDs) == 0 {
		return fmt.Errorf("请至少选择一个服务器")
	}
	if strings.TrimSpace(p.ConfigPackage) == "" {
		return fmt.Errorf("请填写配置包")
	}
	if strings.TrimSpace(p.HotFiles) == "" {
		return fmt.Errorf("请填写热更文件列表")
	}
	return nil
}

func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// ParseMergePairs 解析多行 "目标,源" 文本。
func ParseMergePairs(text string) ([]MergePair, error) {
	var pairs []MergePair
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("格式错误,应为 目标,源: %q", line)
		}
		target, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("目标服ID不是数字: %q", parts[0])
		}
		source, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("源服ID不是数字: %q", parts[1])
		}
		if target == source {
			return nil, fmt.Errorf("目标服与源服ID相同: %d", target)
		}
		pairs = append(pairs, MergePair{Target: target, Source: source})
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("合服列表为空")
	}
	return pairs, nil
}

// ParseServerIDs 解析逗号分隔的服务器ID列表(表单多选 value)。
func ParseServerIDs(values []string) ([]int, error) {
	var ids []int
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		id, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("服务器ID不是数字: %q", v)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("未选择任何服务器")
	}
	return ids, nil
}

// MarshalParams 序列化任意参数结构体为 JSON 字符串。
func MarshalParams(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func UnmarshalMergeParams(s string) (MergeParams, error) {
	var p MergeParams
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}

func UnmarshalReleaseParams(s string) (ReleaseParams, error) {
	var p ReleaseParams
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}

func UnmarshalHotupdateParams(s string) (HotupdateParams, error) {
	var p HotupdateParams
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}

// NewServerRow 冻结的一行待创建服(含部署/建库所需的全部信息)。
type NewServerRow struct {
	Kind         string            `json:"kind"` // game/battle/copy/center
	ID           int               `json:"id"`
	DataBaseUser string            `json:"db_user"`
	DataBasePsw  string            `json:"db_psw"`
	MySqlIp      string            `json:"mysql_ip"`
	MySqlPort    string            `json:"mysql_port"`
	SelfPublicIp string            `json:"self_ip"` // 部署 SSH 目标(内网IP)
	Fields       map[string]string `json:"fields"`  // 完整待插入配置行
}

// NewServerCreateParams 创建游戏服工单的冻结参数(建单时算好,执行/重试只认它)。
type NewServerCreateParams struct {
	RegionName     string         `json:"region_name"`
	VersionPackage string         `json:"version_package"`
	Rows           []NewServerRow `json:"rows"`
	LastFailedIDs  []int          `json:"last_failed_ids,omitempty"` // 上次部署失败的服ID;重试只补这些
}

func (p NewServerCreateParams) Summary() string {
	var ids []int
	for _, r := range p.Rows {
		if r.Kind == "game" {
			ids = append(ids, r.ID)
		}
	}
	return fmt.Sprintf("%s · 新游戏服 %s", p.RegionName, joinInts(ids))
}

func UnmarshalNewServerCreateParams(s string) (NewServerCreateParams, error) {
	var p NewServerCreateParams
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}

// DeleteServerParams 删除游戏服工单的冻结参数(建单时算好,执行/重试只认它)。
type DeleteServerParams struct {
	ID              int               `json:"id"`
	Kind            string            `json:"kind"`        // game/battle/copy/center
	ServerName      string            `json:"server_name"` // 展示用
	SelfPublicIp    string            `json:"self_ip"`     // 停服/丢库/删包的 SSH 目标
	DataBaseName    string            `json:"db_name"`
	MySqlIp         string            `json:"mysql_ip"`
	MySqlPort       string            `json:"mysql_port"`
	DataBaseUser    string            `json:"db_user"`
	DataBasePsw     string            `json:"db_psw"`
	Fields          map[string]string `json:"fields"`           // 供 ALB 取 PortForClient / GlobalCenterWorldID 等
	RemoveALB       bool              `json:"remove_alb"`       // 拆 ALB;建单置 true,执行器再按 ALB.Enabled 决定
	RegenServerlist bool              `json:"regen_serverlist"` // 重生 serverlist;建单置 true
}

func (p DeleteServerParams) Summary() string {
	if p.ServerName == "" {
		return fmt.Sprintf("Id %d", p.ID)
	}
	return fmt.Sprintf("%s(Id %d)", p.ServerName, p.ID)
}

func UnmarshalDeleteServerParams(s string) (DeleteServerParams, error) {
	var p DeleteServerParams
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}

// ParseServerIDsText 解析文本框服号:逗号(中英文)/空白/换行均可分隔。
func ParseServerIDsText(text string) ([]int, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var ids []int
	for _, f := range fields {
		id, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("服务器ID不是数字: %q", f)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("未填写任何服务器ID")
	}
	return ids, nil
}

// MergeDatePlusOneDay 把 YYYYMMDD 加一天(日历进位),返回 YYYYMMDD。
func MergeDatePlusOneDay(yyyymmdd string) (string, error) {
	t, err := time.Parse("20060102", yyyymmdd)
	if err != nil {
		return "", fmt.Errorf("预合服时间格式应为 YYYYMMDD: %q", yyyymmdd)
	}
	return t.AddDate(0, 0, 1).Format("20060102"), nil
}
