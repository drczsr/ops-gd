package order

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gongdan/internal/model"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/workflow"
	"gongdan/internal/settings"

	"gorm.io/gorm"
)

// Service 工单业务。
type Service struct {
	db        *gorm.DB
	settings  *settings.Store
	realCfg   *executor.RealConfig
	execDelay time.Duration // mock 执行的步进延迟,测试时置 0
	seqMu     sync.Mutex    // 串行化单号生成,避免同进程并发撞号
	schedTick time.Duration // 调度器扫描间隔

	notifier      Notifier // 通知发送器,nil=未配置(静默)
	notifyBaseURL string   // 详情链接基础地址,空=不带链接
}

func New(gdb *gorm.DB, st *settings.Store, realCfg *executor.RealConfig) *Service {
	return &Service{db: gdb, settings: st, realCfg: realCfg,
		execDelay: 800 * time.Millisecond, schedTick: 30 * time.Second}
}

// WithAutomation 注入调度器扫描间隔(自动流转开关已迁移到 settings.Store)。
func (s *Service) WithAutomation(schedTick time.Duration) *Service {
	if schedTick > 0 {
		s.schedTick = schedTick
	}
	return s
}

// Create 创建工单,初始状态为待审批,并写一条提交审计日志。
func (s *Service) Create(woType, title, targetSummary, params, createBy string) (*model.WorkOrder, error) {
	now := time.Now()
	wo := &model.WorkOrder{
		OrderNo:       s.nextOrderNo(now),
		Type:          woType,
		Title:         title,
		Status:        workflow.StatusPendingApprove,
		Params:        params,
		TargetSummary: targetSummary,
		CreateBy:      createBy,
		CreateTime:    now,
		UpdateTime:    now,
	}
	if err := s.db.Create(wo).Error; err != nil {
		return nil, err
	}
	s.writeLog(wo.ID, model.LogAction, workflow.StageSubmit, createBy, "提交工单")
	s.notifyEvent(wo, evSubmit, "")
	return wo, nil
}

// CreateAndExecute 建单(免审批)并立即异步执行。用于系统专属、无需人工审批的工单(创建游戏服)。
func (s *Service) CreateAndExecute(woType, title, targetSummary, params, createBy string) (*model.WorkOrder, error) {
	now := time.Now()
	wo := &model.WorkOrder{
		OrderNo:       s.nextOrderNo(now),
		Type:          woType,
		Title:         title,
		Status:        workflow.StatusPendingExecute,
		Params:        params,
		TargetSummary: targetSummary,
		CreateBy:      createBy,
		CreateTime:    now,
		UpdateTime:    now,
	}
	if err := s.db.Create(wo).Error; err != nil {
		return nil, err
	}
	s.writeLog(wo.ID, model.LogAction, workflow.StageSubmit, createBy, "提交工单(系统工单,免审批)")
	s.triggerExecute(wo, "system")
	return wo, nil
}

// SetScheduledTime 设置工单计划执行时刻(提交时调用)。
func (s *Service) SetScheduledTime(id uint, t *time.Time) error {
	return s.db.Model(&model.WorkOrder{}).Where("id = ?", id).
		Update("scheduled_time", t).Error
}

// Get 按 ID 取工单。
func (s *Service) Get(id uint) (*model.WorkOrder, error) {
	var wo model.WorkOrder
	if err := s.db.First(&wo, id).Error; err != nil {
		return nil, err
	}
	return &wo, nil
}

// UpdateNewServerPackage 改新建服失败工单的版本包(仅 TypeNewServer + ExecFailed)。
// 包不进配置库,改完用户再点「重试执行」即用新包重跑失败的服。
func (s *Service) UpdateNewServerPackage(id uint, pkg, operator string) error {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return fmt.Errorf("版本包不能为空")
	}
	wo, err := s.Get(id)
	if err != nil {
		return err
	}
	if wo.Type != model.TypeNewServer {
		return fmt.Errorf("仅新建游戏服工单可换包")
	}
	if wo.Status != workflow.StatusExecFailed {
		return fmt.Errorf("仅执行失败的工单可换包")
	}
	p, err := model.UnmarshalNewServerCreateParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析创建参数: %w", err)
	}
	old := p.VersionPackage
	p.VersionPackage = pkg
	np, err := model.MarshalParams(p)
	if err != nil {
		return err
	}
	wo.Params = np
	wo.UpdateTime = time.Now()
	if err := s.db.Save(wo).Error; err != nil {
		return err
	}
	s.writeLog(id, model.LogAction, workflow.StageExecute, operator,
		fmt.Sprintf("换包:%s → %s", old, pkg))
	return nil
}

// List 列出工单(可按类型/状态过滤),按 ID 倒序。
func (s *Service) List(woType, status string) ([]model.WorkOrder, error) {
	q := s.db.Order("id desc")
	if woType != "" {
		q = q.Where("type = ?", woType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []model.WorkOrder
	err := q.Find(&rows).Error
	return rows, err
}

// Logs 取工单日志(按时间正序)。
func (s *Service) Logs(orderID uint) ([]model.WorkOrderLog, error) {
	var rows []model.WorkOrderLog
	err := s.db.Where("order_id = ?", orderID).Order("id asc").Find(&rows).Error
	return rows, err
}

// LogsAfter 按自增 ID 取增量日志(仅返回 id > afterID 的记录,正序)。
func (s *Service) LogsAfter(orderID, afterID uint) ([]model.WorkOrderLog, error) {
	var rows []model.WorkOrderLog
	err := s.db.Where("order_id = ? AND id > ?", orderID, afterID).Order("id asc").Find(&rows).Error
	return rows, err
}

// ListMine 列出某人提交的工单(可按类型过滤),按 ID 倒序。
func (s *Service) ListMine(createBy, woType string) ([]model.WorkOrder, error) {
	q := s.db.Order("id desc").Where("create_by = ?", createBy)
	if woType != "" {
		q = q.Where("type = ?", woType)
	}
	var rows []model.WorkOrder
	err := q.Find(&rows).Error
	return rows, err
}

// ListExecuted 列出已进入过执行阶段的工单(execute_time 非空),按执行时间倒序;可按类型过滤。
func (s *Service) ListExecuted(woType string) ([]model.WorkOrder, error) {
	q := s.db.Where("execute_time IS NOT NULL").Order("execute_time desc")
	if woType != "" {
		q = q.Where("type = ?", woType)
	}
	var rows []model.WorkOrder
	err := q.Find(&rows).Error
	return rows, err
}

// PendingApproveCount 待审批工单数量(用于侧边栏徽章)。
func (s *Service) PendingApproveCount() int64 {
	var n int64
	s.db.Model(&model.WorkOrder{}).Where("status = ?", workflow.StatusPendingApprove).Count(&n)
	return n
}

// AuditEntry 操作日志一行:动作日志 + 所属工单的摘要信息(扁平结构,便于 Scan)。
type AuditEntry struct {
	ID         uint
	OrderID    uint
	Stage      string
	Operator   string
	Content    string
	CreateTime time.Time
	OrderNo    string
	Type       string
	Title      string
}

// AuditLog 跨工单的动作审计流(只取 action 类日志,排除逐行执行输出),按时间倒序。
func (s *Service) AuditLog(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	var rows []AuditEntry
	err := s.db.Table("work_order_logs as l").
		Select("l.id, l.order_id, l.stage, l.operator, l.content, l.create_time, "+
			"w.order_no, w.type, w.title").
		Joins("left join work_orders w on w.id = l.order_id").
		Where("l.log_type = ?", model.LogAction).
		Order("l.id desc").Limit(limit).Scan(&rows).Error
	return rows, err
}

// isAdmin 是否管理员:内置 admin 账号永远是管理员(防锁死),或被授予管理员角色的成员。
func (s *Service) isAdmin(userID string) bool {
	if userID == model.AdminUsername {
		return true
	}
	var m model.WorkOrderMember
	if err := s.db.Where("user_id = ?", userID).First(&m).Error; err != nil {
		return false
	}
	return m.CanAdmin
}

// Act 对工单执行一个人工动作。execute/open 走异步触发器;
// approve/testPass 成功后按配置(及 scheduled_time)接 auto 钩子。
func (s *Service) Act(id uint, action, operator, remark string) error {
	wo, err := s.Get(id)
	if err != nil {
		return err
	}
	next, ok := workflow.NextStatus(wo.Status, action)
	if !ok {
		return fmt.Errorf("当前状态[%s]不允许动作[%s]", wo.Status, action)
	}
	// 提交人不能审批自己的工单(管理员豁免)
	if (action == workflow.ActionApprove || action == workflow.ActionReject) &&
		operator == wo.CreateBy && !s.isAdmin(operator) {
		return fmt.Errorf("提交人不能审批自己的工单")
	}
	// 作废权限:仅允许提交人本人作废;管理员可作废任意工单
	if action == workflow.ActionCancel &&
		operator != wo.CreateBy && !s.isAdmin(operator) {
		return fmt.Errorf("仅提交人本人可作废工单")
	}
	// 重新提交权限:仅允许提交人本人重新提交;管理员可代为重新提交
	if action == workflow.ActionResubmit &&
		operator != wo.CreateBy && !s.isAdmin(operator) {
		return fmt.Errorf("仅提交人本人可重新提交工单")
	}

	// execute / open:不在此落库为中间态,交给异步触发器(它会置 executing/opening 并起协程)
	if action == workflow.ActionExecute {
		s.triggerExecute(wo, operator)
		return nil
	}
	if action == workflow.ActionOpen {
		s.triggerOpen(wo, operator)
		return nil
	}
	if action == workflow.ActionTeardown {
		if wo.Type != model.TypeNewServer {
			return fmt.Errorf("仅新建游戏服工单可回滚清理")
		}
		s.triggerTeardown(wo, operator)
		return nil
	}

	now := time.Now()
	wo.Status = next
	wo.UpdateTime = now
	switch action {
	case workflow.ActionApprove, workflow.ActionReject:
		wo.ApproveBy = operator
		wo.ApproveTime = &now
		wo.ApproveRemark = remark
	case workflow.ActionTestPass, workflow.ActionTestFail:
		wo.TestBy = operator
		wo.TestTime = &now
		wo.TestRemark = remark
	}
	if err := s.db.Save(wo).Error; err != nil {
		return err
	}
	s.writeLog(wo.ID, model.LogAction, workflow.StageOf(next), operator,
		actionLabel(action)+remarkSuffix(remark))

	switch action {
	case workflow.ActionApprove:
		s.notifyEvent(wo, evApproved, "")
	case workflow.ActionReject:
		s.notifyEvent(wo, evRejected, remarkSuffix(remark))
	case workflow.ActionTestPass:
		s.notifyEvent(wo, evTestPass, "")
	case workflow.ActionTestFail:
		s.notifyEvent(wo, evTestFail, remarkSuffix(remark))
	}

	// auto 钩子
	switch action {
	case workflow.ActionApprove:
		// 有计划时间 → 交调度器到点跑;否则按 auto_execute 决定是否立即跑
		if wo.ScheduledTime == nil && s.settings.AutoExecute() {
			s.writeLog(wo.ID, model.LogAction, workflow.StageExecute, "system", "自动执行(配置触发)")
			// wo 已落库为 pending_execute;复用同一指针,triggerExecute 继续变更并再次 Save
			s.triggerExecute(wo, "system")
		}
	case workflow.ActionTestPass:
		if s.settings.AutoOpen() {
			s.writeLog(wo.ID, model.LogAction, workflow.StageOpen, "system", "自动开放(配置触发)")
			s.triggerOpen(wo, "system")
		}
	}
	return nil
}

// failedTargetsReporter 由发版/热更执行器实现:暴露本次未成功的服ID。
type failedTargetsReporter interface {
	FailedTargets() []int
}

// hotFailedReporter 仅发版执行器实现:暴露热更失败主机IP。
type hotFailedReporter interface {
	HotFailedHosts() []string
}

// configFailedReporter 仅发版执行器实现:暴露配置更新失败服ID。
type configFailedReporter interface {
	ConfigFailedTargets() []int
}

// applyLastFailed 把执行器报告的失败子集写回工单参数JSON(成功则清空)。
// 按工单类型选择正确的参数结构,避免热更参数被当成发版参数序列化而丢字段。
// 执行器未实现 failedTargetsReporter(合服/mock)或解析失败时,原样返回。
func applyLastFailed(woType, params string, exec executor.Executor) string {
	fr, ok := exec.(failedTargetsReporter)
	if !ok {
		return params
	}
	switch woType {
	case model.TypeRelease:
		p, err := model.UnmarshalReleaseParams(params)
		if err != nil {
			return params
		}
		p.LastFailedIDs = fr.FailedTargets()
		if hr, ok := exec.(hotFailedReporter); ok {
			p.HotFailedHosts = hr.HotFailedHosts()
		}
		if cr, ok := exec.(configFailedReporter); ok {
			p.ConfigFailedIDs = cr.ConfigFailedTargets()
		}
		out, err := model.MarshalParams(p)
		if err != nil {
			return params
		}
		return out
	case model.TypeHotupdate:
		p, err := model.UnmarshalHotupdateParams(params)
		if err != nil {
			return params
		}
		p.LastFailedIDs = fr.FailedTargets()
		out, err := model.MarshalParams(p)
		if err != nil {
			return params
		}
		return out
	case model.TypeNewServer:
		p, err := model.UnmarshalNewServerCreateParams(params)
		if err != nil {
			return params
		}
		p.LastFailedIDs = fr.FailedTargets()
		out, err := model.MarshalParams(p)
		if err != nil {
			return params
		}
		return out
	default:
		return params
	}
}

// runExecutor 异步执行,实时写 exec 日志,结束后更新状态。
func (s *Service) runExecutor(id uint) {
	wo, err := s.Get(id)
	if err != nil {
		return
	}
	exec := executor.Dispatch(s.settings.Mode(), wo.Type, s.realCfg)
	if m, ok := exec.(*executor.MockExecutor); ok {
		m.StepDelay = s.execDelay
	}

	var logMu sync.Mutex
	logFn := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		s.writeLog(id, model.LogExec, workflow.StageExecute, "system", line)
	}

	execErr := exec.Execute(wo, logFn)

	now := time.Now()
	cur, _ := s.Get(id)
	cur.Params = applyLastFailed(cur.Type, cur.Params, exec) // 写回本次失败子集(成功则清空)
	if execErr != nil {
		next, _ := workflow.NextStatus(cur.Status, workflow.ActionExecFail)
		cur.Status = next
		cur.UpdateTime = now
		s.db.Save(cur)
		s.writeLog(id, model.LogAction, workflow.StageExecute, "system", "执行失败: "+execErr.Error())
		s.notifyEvent(cur, evExecFailed, "失败:"+execErr.Error())
		return
	}
	if cur.Type == model.TypeHotupdate || cur.Type == model.TypeConfigPush || cur.Type == model.TypeMergePublish || cur.Type == model.TypeNewServer || cur.Type == model.TypeDeleteServer {
		// 热更/全服更新/合服预告发布:执行完成即完成,无测试/开放阶段
		cur.Status = workflow.StatusOpened
		cur.ExecuteEndTime = &now
		cur.OpenTime = &now
		cur.UpdateTime = now
		s.db.Save(cur)
		s.writeLog(id, model.LogAction, workflow.StageDone, "system", "执行完成,工单完成(无需测试/开放)")
		s.notifyEvent(cur, evOpenSuccess, "")
		return
	}
	next, _ := workflow.NextStatus(cur.Status, workflow.ActionExecSuccess)
	cur.Status = next
	cur.ExecuteEndTime = &now
	cur.UpdateTime = now
	s.db.Save(cur)
	s.writeLog(id, model.LogAction, workflow.StageTest, "system", "执行完成")
	s.notifyEvent(cur, evExecSuccess, "")
}

// triggerExecute 把工单置执行中并异步跑执行器(人工/自动/定时三入口共用)。
func (s *Service) triggerExecute(wo *model.WorkOrder, operator string) {
	now := time.Now()
	wo.Status = workflow.StatusExecuting
	wo.ExecuteBy = operator
	wo.ExecuteTime = &now
	wo.UpdateTime = now
	s.db.Save(wo)
	s.writeLog(wo.ID, model.LogAction, workflow.StageExecute, operator, "开始执行")
	go s.runExecutor(wo.ID)
}

// triggerTeardown 置中间态并异步跑回滚清理(仅新建服失败工单)。
func (s *Service) triggerTeardown(wo *model.WorkOrder, operator string) {
	now := time.Now()
	wo.Status = workflow.StatusExecuting // 复用「执行中」作中间态
	wo.UpdateTime = now
	s.db.Save(wo)
	s.writeLog(wo.ID, model.LogAction, workflow.StageExecute, operator, "开始回滚清理")
	go s.runTeardown(wo.ID, operator)
}

// runTeardown 异步拆除:mock 模式直接成功;real 模式跑 RollbackNewServerExecutor。
// 成功 → 作废;失败(未清净)→ 退回执行失败,可再点回滚。
func (s *Service) runTeardown(id uint, operator string) {
	wo, err := s.Get(id)
	if err != nil {
		return
	}
	logMu := &sync.Mutex{}
	logFn := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		s.writeLog(id, model.LogExec, workflow.StageExecute, "system", line)
	}
	var rbErr error
	if s.settings.Mode() == "real" {
		rbErr = executor.NewRollbackNewServerExecutor(s.realCfg).Execute(wo, logFn)
	} else {
		logFn("mock 模式:跳过真实拆除")
	}
	now := time.Now()
	cur, _ := s.Get(id)
	if rbErr != nil {
		cur.Status = workflow.StatusExecFailed
		cur.UpdateTime = now
		s.db.Save(cur)
		s.writeLog(id, model.LogAction, workflow.StageExecute, "system", "回滚清理未完成: "+rbErr.Error())
		return
	}
	cur.Status = workflow.StatusCancelled
	cur.UpdateTime = now
	s.db.Save(cur)
	s.writeLog(id, model.LogAction, workflow.StageDone, "system", "回滚清理完成,工单作废")
}

// triggerOpen 把工单置开放中并异步跑开放(人工/自动共用)。
func (s *Service) triggerOpen(wo *model.WorkOrder, operator string) {
	now := time.Now()
	wo.Status = workflow.StatusOpening
	wo.OpenBy = operator
	wo.UpdateTime = now
	s.db.Save(wo)
	s.writeLog(wo.ID, model.LogAction, workflow.StageOpen, operator, "开始开放")
	go s.runOpen(wo.ID)
}

// runOpen 异步执行开放阶段:发版/合服跑真脚本,其它类型(mock)直接成功。
func (s *Service) runOpen(id uint) {
	wo, err := s.Get(id)
	if err != nil {
		return
	}
	exec := executor.Dispatch(s.settings.Mode(), wo.Type, s.realCfg)
	var logMu sync.Mutex
	logFn := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		s.writeLog(id, model.LogExec, workflow.StageOpen, "system", line)
	}
	var openErr error
	if opener, ok := exec.(executor.OpenRunner); ok {
		openErr = opener.Open(wo, logFn)
	}
	now := time.Now()
	cur, _ := s.Get(id)
	if openErr != nil {
		cur.Status = workflow.StatusOpenFailed
		cur.UpdateTime = now
		s.db.Save(cur)
		s.writeLog(id, model.LogAction, workflow.StageOpen, "system", "开放失败: "+openErr.Error())
		s.notifyEvent(cur, evOpenFailed, "失败:"+openErr.Error())
		return
	}
	cur.Status = workflow.StatusOpened
	cur.OpenTime = &now
	cur.UpdateTime = now
	s.db.Save(cur)
	s.writeLog(id, model.LogAction, workflow.StageDone, "system", "开放完成")
	s.notifyEvent(cur, evOpenSuccess, "")
}

func (s *Service) writeLog(orderID uint, logType, stage, operator, content string) {
	s.db.Create(&model.WorkOrderLog{
		OrderID:    orderID,
		LogType:    logType,
		Stage:      stage,
		Operator:   operator,
		Content:    content,
		CreateTime: time.Now(),
	})
}

// nextOrderNo 生成形如 WO20260529<序号> 的编号。
// 序号基于数据库中当天已有工单数推算,因此进程重启后不会与历史工单撞号。
func (s *Service) nextOrderNo(now time.Time) string {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	prefix := "WO" + now.Format("20060102")
	var count int64
	s.db.Model(&model.WorkOrder{}).Where("order_no LIKE ?", prefix+"%").Count(&count)
	return fmt.Sprintf("%s%03d", prefix, count+1)
}

func actionLabel(action string) string {
	switch action {
	case workflow.ActionApprove:
		return "审批通过"
	case workflow.ActionReject:
		return "审批驳回"
	case workflow.ActionResubmit:
		return "重新提交"
	case workflow.ActionExecute:
		return "开始执行"
	case workflow.ActionTestPass:
		return "测试通过"
	case workflow.ActionTestFail:
		return "测试不通过"
	case workflow.ActionOpen:
		return "开放"
	case workflow.ActionCancel:
		return "作废"
	default:
		return action
	}
}

func remarkSuffix(remark string) string {
	if remark == "" {
		return ""
	}
	return ": " + remark
}

// scanDue 触发所有"待执行 + 已到计划时刻"的工单(定时执行)。
func (s *Service) scanDue() {
	var rows []model.WorkOrder
	s.db.Where("status = ? AND scheduled_time IS NOT NULL AND scheduled_time <= ?",
		workflow.StatusPendingExecute, time.Now()).Find(&rows)
	for i := range rows {
		wo := rows[i]
		s.writeLog(wo.ID, model.LogAction, workflow.StageExecute, "system", "定时触发执行")
		s.triggerExecute(&wo, "system")
	}
}

// StartScheduler 启动后台轮询调度器,每 schedTick 扫一次到点的定时工单。
func (s *Service) StartScheduler(ctx context.Context) {
	tick := s.schedTick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.scanDue()
			}
		}
	}()
}
