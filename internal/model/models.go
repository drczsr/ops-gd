package model

import (
	"strings"
	"time"
)

// targetPreviewN 列表页「目标」列折叠阈值:服号超过此数量只显示前几个 + 更多。
const targetPreviewN = 6

// TargetView 列表页「目标」列的折叠视图。
type TargetView struct {
	Full    string // 完整内容(展开后显示)
	Preview string // 折叠时的前几项预览(Fold=true 才有意义)
	Count   int    // 项数
	Fold    bool   // 是否需要折叠
}

// TargetBrief 把目标摘要(逗号分隔的服号串,或如"合服 2 组"的短摘要)
// 转成折叠视图:项数 <= targetPreviewN 原样显示,超过则预览前 3 个 + 更多。
func TargetBrief(summary string) TargetView {
	parts := strings.Split(summary, ",")
	if len(parts) <= targetPreviewN {
		return TargetView{Full: summary, Count: len(parts)}
	}
	return TargetView{
		Full:    summary,
		Preview: strings.Join(parts[:3], ","),
		Count:   len(parts),
		Fold:    true,
	}
}

// AdminUsername 超级管理员账号名;该账号豁免"提交人不能审批自己工单"的限制。
const AdminUsername = "admin"

// 工单类型
const (
	TypeMerge        = "merge"        // 合服
	TypeRelease      = "release"      // 发版
	TypeHotupdate    = "hotupdate"    // 热更
	TypeConfigPush   = "configpush"   // 全服更新(系统生成)
	TypeMergePublish = "mergepublish" // 合服预告发布(系统生成)
	TypeNewServer    = "newserver"    // 创建游戏服(系统生成)
	TypeDeleteServer = "deleteserver" // 删除游戏服(系统生成)
)

// typeLabels 工单类型内部值 -> 中文展示名。
var typeLabels = map[string]string{
	TypeMerge:        "合服",
	TypeRelease:      "发版",
	TypeHotupdate:    "热更",
	TypeConfigPush:   "全服更新",
	TypeMergePublish: "合服预告发布",
	TypeNewServer:    "创建游戏服",
	TypeDeleteServer: "删除游戏服",
}

// TypeLabel 返回工单类型的中文展示名;未知类型原样返回。
func TypeLabel(t string) string {
	if cn, ok := typeLabels[t]; ok {
		return cn
	}
	return t
}

// Badge 列表/详情页用的彩色徽章(中文名 + Bootstrap 配色类 + 图标)。
type Badge struct {
	Label string // 中文名
	Class string // Bootstrap text-bg-* 配色类
	Icon  string // 前缀图标(emoji,可为空)
}

// typeBadges 工单类型 -> 徽章样式类(淡色 soft 风格)+ 线性 SVG 图标符号id(见 layout.html 精灵)。
var typeBadges = map[string]Badge{
	TypeMerge:        {Class: "t-merge", Icon: "ic-t-merge"},
	TypeRelease:      {Class: "t-release", Icon: "ic-t-release"},
	TypeHotupdate:    {Class: "t-hotupdate", Icon: "ic-t-hot"},
	TypeConfigPush:   {Class: "t-configpush", Icon: "ic-t-config"},
	TypeMergePublish: {Class: "t-mergepublish", Icon: "ic-t-config"},
	TypeNewServer:    {Class: "t-newserver", Icon: "ic-server"},
	TypeDeleteServer: {Class: "t-deleteserver", Icon: "ic-server"},
}

// TypeBadge 返回工单类型的淡色徽章;未知类型回退为中性灰、无图标、标签原样。
func TypeBadge(t string) Badge {
	b, ok := typeBadges[t]
	if !ok {
		return Badge{Label: t, Class: "t-default"}
	}
	b.Label = TypeLabel(t)
	return b
}

// WorkOrder 工单主表。
type WorkOrder struct {
	ID             uint   `gorm:"primaryKey"`
	OrderNo        string `gorm:"uniqueIndex;size:32"`
	Type           string `gorm:"size:16;index"`
	Title          string `gorm:"size:128"`
	Status         string `gorm:"size:32;index"`
	Params         string `gorm:"type:text"` // 类型专属参数(JSON)
	TargetSummary  string `gorm:"size:255"`
	CreateBy       string `gorm:"size:64"`
	CreateTime     time.Time
	ScheduledTime  *time.Time // 计划执行时刻;nil=立即(由 auto/manual 配置决定)
	ApproveBy      string     `gorm:"size:64"`
	ApproveTime    *time.Time
	ApproveRemark  string `gorm:"size:255"`
	ExecuteBy      string `gorm:"size:64"`
	ExecuteTime    *time.Time
	ExecuteEndTime *time.Time
	TestBy         string `gorm:"size:64"`
	TestTime       *time.Time
	TestRemark     string `gorm:"size:255"`
	OpenBy         string `gorm:"size:64"`
	OpenTime       *time.Time
	Remark         string `gorm:"size:255"`
	UpdateTime     time.Time
}

// 日志类型
const (
	LogAction = "action" // 阶段动作(审计)
	LogExec   = "exec"   // 执行脚本输出
)

// WorkOrderLog 工单日志(审计链 + 执行日志合一)。
type WorkOrderLog struct {
	ID         uint   `gorm:"primaryKey"`
	OrderID    uint   `gorm:"index"`
	LogType    string `gorm:"size:16"`
	Stage      string `gorm:"size:32"`
	Operator   string `gorm:"size:64"`
	Content    string `gorm:"type:text"`
	CreateTime time.Time
}

// WorkOrderArtifact 配置类工单(configpush/mergepublish)的快照工件:
// 建单时冻结将下发的文件 + 建单时线上版本 id;执行成功后回填部署版本 id。
type WorkOrderArtifact struct {
	ID                uint   `gorm:"primaryKey"`
	OrderID           uint   `gorm:"uniqueIndex"`
	PrimaryKey        string `gorm:"size:255"`  // 主文件 COS 对象键(diff/基准校验/回填用)
	SnapshotFiles     string `gorm:"type:text"` // JSON: {cosKey: base64(原始字节)}
	BaselineVersionID string `gorm:"size:128"`  // 建单时线上版本 id(空=当时线上无此文件)
	DeployedVersionID string `gorm:"size:128"`  // 执行成功后回填,供历史/回滚
}

// WorkOrderMember 角色标签表。
type WorkOrderMember struct {
	ID          uint   `gorm:"primaryKey"`
	UserID      string `gorm:"uniqueIndex;size:64"`
	DisplayName string `gorm:"size:64"`
	CanSubmit   bool
	CanApprove  bool
	CanExecute  bool
	CanTest     bool
	CanAdmin    bool // 管理员:系统设置/用户管理/终端,及审批自己工单的豁免;并 override 全部菜单
	// 菜单访问标志(admin 永远通行,无需勾):
	CanServers      bool // 服务器管理
	CanGSConfig     bool // 游戏服配置
	CanMergePreview bool // 合服预告
}

// User 登录账号(账号密码登录)。
type User struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;size:64"`
	Password     string `gorm:"size:255"` // bcrypt 哈希
	DisplayName  string `gorm:"size:64"`
	WeworkUserID string `gorm:"size:64"`
}
