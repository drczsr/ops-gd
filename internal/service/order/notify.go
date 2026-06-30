package order

import (
	"fmt"
	"log"
	"strings"

	"gongdan/internal/model"
)

// Notifier 通知发送器;*notify.WeworkBot 实现之。nil = 未配置,静默跳过。
type Notifier interface {
	Send(text string, mentionUserIDs []string) error
}

// 通知事件
const (
	evSubmit      = "submit"      // →待审批,@审批人
	evApproved    = "approved"    // →待执行,@执行人
	evRejected    = "rejected"    // →已驳回,@提交人
	evExecFailed  = "execFailed"  // →执行失败,@执行人+提交人
	evExecSuccess = "execSuccess" // →待测试,@测试人
	evTestPass    = "testPass"    // →待开放,@执行人
	evTestFail    = "testFail"    // →退回待执行,@执行人
	evOpenFailed  = "openFailed"  // →开放失败,@执行人
	evOpenSuccess = "openSuccess" // →已开放(完成),@提交人
)

// WithNotify 注入通知发送器与详情链接基础地址(nil notifier = 关闭)。
func (s *Service) WithNotify(n Notifier, baseURL string) *Service {
	s.notifier = n
	s.notifyBaseURL = baseURL
	return s
}

// notifyEvent 据事件拼文案、解析 @目标,异步发送;失败仅记日志,绝不阻塞主流程。
func (s *Service) notifyEvent(wo *model.WorkOrder, event, extra string) {
	if s.notifier == nil {
		return
	}
	var headline string
	var mentions []string
	switch event {
	case evSubmit:
		headline = "【工单提交】待审批"
		mentions = s.resolveByRole("can_approve")
	case evApproved:
		headline = "【审批通过】待执行"
		mentions = s.resolveByRole("can_execute")
	case evRejected:
		headline = "【已驳回】"
		mentions = s.resolveByUsername(wo.CreateBy)
	case evExecFailed:
		headline = "【执行失败】"
		mentions = append(s.resolveByRole("can_execute"), s.resolveByUsername(wo.CreateBy)...)
	case evExecSuccess:
		headline = "【执行完成】待测试"
		mentions = s.resolveByRole("can_test")
	case evTestPass:
		headline = "【测试通过】待开放"
		mentions = s.resolveByRole("can_execute")
	case evTestFail:
		headline = "【测试不通过】退回待执行"
		mentions = s.resolveByRole("can_execute")
	case evOpenFailed:
		headline = "【开放失败】"
		mentions = s.resolveByRole("can_execute")
	case evOpenSuccess:
		headline = "【已开放·完成】"
		mentions = s.resolveByUsername(wo.CreateBy)
	default:
		return
	}
	text := s.buildText(wo, headline, extra)
	go func(text string, mentions []string, orderNo string) {
		if err := s.notifier.Send(text, mentions); err != nil {
			log.Printf("[notify] order %s send failed: %v", orderNo, err)
		}
	}(text, mentions, wo.OrderNo)
}

// buildText 拼装中文纯文本消息(多行)。
func (s *Service) buildText(wo *model.WorkOrder, headline, extra string) string {
	var b strings.Builder
	b.WriteString(headline + "\n")
	b.WriteString("单号:" + wo.OrderNo + "\n")
	b.WriteString("类型:" + model.TypeLabel(wo.Type) + " / 标题:" + wo.Title + "\n")
	if wo.TargetSummary != "" {
		b.WriteString("目标:" + wo.TargetSummary + "\n")
	}
	if extra != "" {
		b.WriteString(extra + "\n")
	}
	if s.notifyBaseURL != "" {
		b.WriteString(fmt.Sprintf("详情:%s/orders/%d", strings.TrimRight(s.notifyBaseURL, "/"), wo.ID))
	}
	return strings.TrimRight(b.String(), "\n")
}

// resolveByRole 取拥有指定角色标志(列名,如 can_approve)且 WeworkUserID 非空的成员 userid。
func (s *Service) resolveByRole(col string) []string {
	var usernames []string
	s.db.Model(&model.WorkOrderMember{}).Where(col+" = ?", true).Pluck("user_id", &usernames)
	if len(usernames) == 0 {
		return nil
	}
	var wework []string
	s.db.Model(&model.User{}).
		Where("username IN ? AND wework_user_id <> ''", usernames).
		Pluck("wework_user_id", &wework)
	return wework
}

// resolveByUsername 取单个用户名的非空 WeworkUserID(0 或 1 个)。
func (s *Service) resolveByUsername(username string) []string {
	var wework []string
	s.db.Model(&model.User{}).
		Where("username = ? AND wework_user_id <> ''", username).
		Limit(1).Pluck("wework_user_id", &wework)
	return wework
}
