package workflow

// 状态
const (
	StatusPendingApprove = "pending_approve" // 待审批
	StatusRejected       = "rejected"        // 已驳回
	StatusPendingExecute = "pending_execute" // 待执行
	StatusExecuting      = "executing"       // 执行中
	StatusExecFailed     = "exec_failed"     // 执行失败
	StatusPendingTest    = "pending_test"    // 待测试
	StatusPendingOpen    = "pending_open"    // 待开放
	StatusOpening        = "opening"         // 开放中
	StatusOpenFailed     = "open_failed"     // 开放失败
	StatusOpened         = "opened"          // 已开放(完成)
	StatusCancelled      = "cancelled"       // 已作废
)

// statusLabels 状态机内部值 -> 中文展示名。
var statusLabels = map[string]string{
	StatusPendingApprove: "待审批",
	StatusRejected:       "已驳回",
	StatusPendingExecute: "待执行",
	StatusExecuting:      "执行中",
	StatusExecFailed:     "执行失败",
	StatusPendingTest:    "待测试",
	StatusPendingOpen:    "待开放",
	StatusOpening:        "开放中",
	StatusOpenFailed:     "开放失败",
	StatusOpened:         "已开放",
	StatusCancelled:      "已作废",
}

// StatusLabel 返回状态的中文展示名;未知状态原样返回。
func StatusLabel(status string) string {
	if cn, ok := statusLabels[status]; ok {
		return cn
	}
	return status
}

// statusClasses 状态 -> Bootstrap text-bg-* 配色类。
// 进行中=蓝、待处理=橙、完成=绿、失败/驳回=红、终止/排队=灰。
var statusClasses = map[string]string{
	StatusPendingApprove: "text-bg-warning",
	StatusRejected:       "text-bg-danger",
	StatusPendingExecute: "text-bg-secondary",
	StatusExecuting:      "text-bg-primary",
	StatusExecFailed:     "text-bg-danger",
	StatusPendingTest:    "text-bg-info",
	StatusPendingOpen:    "text-bg-secondary",
	StatusOpening:        "text-bg-primary",
	StatusOpenFailed:     "text-bg-danger",
	StatusOpened:         "text-bg-success",
	StatusCancelled:      "text-bg-secondary",
}

// StatusClass 返回状态徽章的 Bootstrap 配色类;未知状态用灰底。
func StatusClass(status string) string {
	if c, ok := statusClasses[status]; ok {
		return c
	}
	return "text-bg-secondary"
}

// 阶段
const (
	StageSubmit  = "submit"
	StageApprove = "approve"
	StageExecute = "execute"
	StageTest    = "test"
	StageOpen    = "open"
	StageDone    = "done"
)

// 动作
const (
	ActionApprove     = "approve"
	ActionReject      = "reject"
	ActionResubmit    = "resubmit"
	ActionExecute     = "execute"
	ActionExecSuccess = "execSuccess" // 系统:执行成功
	ActionExecFail    = "execFail"    // 系统:执行失败
	ActionTestPass    = "testPass"
	ActionTestFail    = "testFail"
	ActionOpen        = "open"
	ActionOpenSuccess = "openSuccess" // 系统:开放成功
	ActionOpenFail    = "openFail"    // 系统:开放失败
	ActionCancel      = "cancel"
	ActionTeardown    = "teardown" // 新建服失败:拆除本单已创建的服并作废
)

// 角色标签
const (
	RoleSubmit  = "submit"
	RoleApprove = "approve"
	RoleExecute = "execute"
	RoleTest    = "test"
	RoleSystem  = "system" // 系统动作,无需人工角色
)

type transition struct {
	from string
	to   string
}

var transitions = map[string]transition{
	ActionApprove:     {StatusPendingApprove, StatusPendingExecute},
	ActionReject:      {StatusPendingApprove, StatusRejected},
	ActionResubmit:    {StatusRejected, StatusPendingApprove},
	ActionExecute:     {StatusPendingExecute, StatusExecuting}, // exec_failed 见 NextStatus 特判
	ActionExecSuccess: {StatusExecuting, StatusPendingTest},
	ActionExecFail:    {StatusExecuting, StatusExecFailed},
	ActionTestPass:    {StatusPendingTest, StatusPendingOpen},
	ActionTestFail:    {StatusPendingTest, StatusPendingExecute},
	ActionOpen:        {StatusPendingOpen, StatusOpening}, // open_failed 见 NextStatus 特判
	ActionOpenSuccess: {StatusOpening, StatusOpened},
	ActionOpenFail:    {StatusOpening, StatusOpenFailed},
}

// NextStatus 返回从 from 执行 action 后的目标状态;非法流转返回 ok=false。
func NextStatus(from, action string) (string, bool) {
	// 作废:任意非终态都可作废
	if action == ActionCancel {
		if from == StatusOpened || from == StatusCancelled {
			return "", false
		}
		return StatusCancelled, true
	}
	// 重试:执行失败态也可触发 execute
	if action == ActionExecute && from == StatusExecFailed {
		return StatusExecuting, true
	}
	// 新建服回滚清理:执行失败态可触发拆除(中间态复用 executing)
	if action == ActionTeardown && from == StatusExecFailed {
		return StatusExecuting, true
	}
	// 重试:开放失败态也可触发 open
	if action == ActionOpen && from == StatusOpenFailed {
		return StatusOpening, true
	}
	t, ok := transitions[action]
	if !ok || t.from != from {
		return "", false
	}
	return t.to, true
}

// RequiredRole 返回执行某动作所需的角色标签。
func RequiredRole(action string) string {
	switch action {
	case ActionApprove, ActionReject:
		return RoleApprove
	case ActionExecute, ActionTeardown:
		return RoleExecute
	case ActionTestPass, ActionTestFail:
		return RoleTest
	case ActionResubmit, ActionCancel:
		return RoleSubmit
	case ActionOpen:
		return RoleExecute // 开放由执行人操作
	case ActionExecSuccess, ActionExecFail:
		return RoleSystem
	case ActionOpenSuccess, ActionOpenFail:
		return RoleSystem
	default:
		return RoleSystem
	}
}

// StageItem 进度条上的一个主阶段。
type StageItem struct {
	Key   string
	Label string
}

// MainStages 详情页进度条的主阶段序列(提交→审批→执行→测试→开放)。
func MainStages() []StageItem {
	return []StageItem{
		{StageSubmit, "提交"},
		{StageApprove, "审批"},
		{StageExecute, "执行"},
		{StageTest, "测试"},
		{StageOpen, "开放"},
	}
}

// StatusStep 返回当前状态在主阶段序列中的"当前活动序号"(0..4),
// 已开放(完成)返回 5(全部走完);驳回/作废等不入进度条的状态返回 -1。
// 用于详情页进度条:序号 < 返回值=已完成、== 返回值=进行中、> 返回值=未到。
func StatusStep(status string) int {
	switch status {
	case StatusPendingApprove:
		return 1
	case StatusPendingExecute, StatusExecuting, StatusExecFailed:
		return 2
	case StatusPendingTest:
		return 3
	case StatusPendingOpen, StatusOpening, StatusOpenFailed:
		return 4
	case StatusOpened:
		return 5
	default: // rejected / cancelled / 未知
		return -1
	}
}

// stageLabels 阶段内部值 -> 中文展示名。
var stageLabels = map[string]string{
	StageSubmit:  "提交",
	StageApprove: "审批",
	StageExecute: "执行",
	StageTest:    "测试",
	StageOpen:    "开放",
	StageDone:    "完成",
}

// StageLabel 返回阶段的中文展示名;未知阶段原样返回。
func StageLabel(stage string) string {
	if cn, ok := stageLabels[stage]; ok {
		return cn
	}
	return stage
}

// StageOf 返回状态所属阶段。
func StageOf(status string) string {
	switch status {
	case StatusPendingApprove:
		return StageApprove
	case StatusRejected:
		return StageSubmit
	case StatusPendingExecute, StatusExecuting, StatusExecFailed:
		return StageExecute
	case StatusPendingTest:
		return StageTest
	case StatusPendingOpen, StatusOpening, StatusOpenFailed:
		return StageOpen
	case StatusOpened, StatusCancelled:
		return StageDone
	default:
		return ""
	}
}
