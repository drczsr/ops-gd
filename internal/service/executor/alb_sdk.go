package executor

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	alb "github.com/alibabacloud-go/alb-20200616/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/client"
	openapiv2 "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/aliyun/credentials-go/credentials"

	"gongdan/internal/model"
)

const (
	defaultALBRegion = "cn-hangzhou"

	defaultResourceGroupID       = "rg-xxxxxxxxxxxx"
	defaultVPCID                 = "vpc-xxxxxxxxxxxx"
	defaultHealthCheckTemplateID = "hct-xxxxxxxxxxxx"

	defaultLoginLoadBalancerID = "alb-xxxxxxxxxxxx"
	defaultLoginListenerID     = "lsn-xxxxxxxxxxxx"
	defaultPayLoadBalancerID   = "alb-yyyyyyyyyyyy"
	defaultPayListenerID       = "lsn-yyyyyyyyyyyy"
	defaultWSLoadBalancerID    = "alb-zzzzzzzzzzzz"
	defaultWSListenerID        = "lsn-zzzzzzzzzzzz"
)

const (
	wsCopyBackendPort   int32 = 3941
	wsBattleBackendPort int32 = 3841
)

const (
	serverGroupNamePrefix = "GAME-GS-"
	serverGroupPaySuffix  = "pay"
)

const (
	serverGroupReadyRetryMaxAttempts = 8
	serverGroupReadyRetryDelay       = 500 * time.Millisecond
	asyncPollMaxAttempts             = 60
	asyncPollDelay                   = 500 * time.Millisecond
)

var albRetrySleep = time.Sleep

type albSDKClient interface {
	ListRules(request *alb.ListRulesRequest) (*alb.ListRulesResponse, error)
	ListServerGroups(request *alb.ListServerGroupsRequest) (*alb.ListServerGroupsResponse, error)
	DeleteRule(request *alb.DeleteRuleRequest) (*alb.DeleteRuleResponse, error)
	ListServerGroupServers(request *alb.ListServerGroupServersRequest) (*alb.ListServerGroupServersResponse, error)
	RemoveServersFromServerGroup(request *alb.RemoveServersFromServerGroupRequest) (*alb.RemoveServersFromServerGroupResponse, error)
	DeleteServerGroup(request *alb.DeleteServerGroupRequest) (*alb.DeleteServerGroupResponse, error)

	CreateServerGroup(request *alb.CreateServerGroupRequest) (*alb.CreateServerGroupResponse, error)
	ApplyHealthCheckTemplateToServerGroup(request *alb.ApplyHealthCheckTemplateToServerGroupRequest) (*alb.ApplyHealthCheckTemplateToServerGroupResponse, error)
	AddServersToServerGroup(request *alb.AddServersToServerGroupRequest) (*alb.AddServersToServerGroupResponse, error)
	CreateRule(request *alb.CreateRuleRequest) (*alb.CreateRuleResponse, error)
}

type ecsSDKClient interface {
	DescribeInstances(request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error)
}

type albTarget struct {
	name           string
	loadBalancerID string
	listenerID     string
}

type albServerRef struct {
	serverID   string
	serverType string
	port       int32
}

// ALBSDKTeardown now handles both setup and teardown when mode=sdk.
type ALBSDKTeardown struct {
	albClient albSDKClient
	ecsClient ecsSDKClient

	region                string
	resourceGroupID       string
	vpcID                 string
	healthCheckTemplateID string

	login albTarget
	pay   albTarget
	ws    albTarget
}

func NewALBSDKTeardown(cfg *ALBConfig) (*ALBSDKTeardown, error) {
	region := fallback(cfg.Region, defaultALBRegion)
	cred, err := credentials.NewCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("load aliyun credential: %w", err)
	}

	openCfgALB := &openapi.Config{RegionId: strp(region), Credential: cred}
	albClient, err := alb.NewClient(openCfgALB)
	if err != nil {
		return nil, fmt.Errorf("init ALB SDK client: %w", err)
	}
	openCfgECS := &openapiv2.Config{RegionId: strp(region), Credential: cred}
	ecsClient, err := ecs.NewClient(openCfgECS)
	if err != nil {
		return nil, fmt.Errorf("init ECS SDK client: %w", err)
	}

	return &ALBSDKTeardown{
		albClient:             albClient,
		ecsClient:             ecsClient,
		region:                region,
		resourceGroupID:       fallback(cfg.ResourceGroupID, defaultResourceGroupID),
		vpcID:                 fallback(cfg.VpcID, defaultVPCID),
		healthCheckTemplateID: fallback(cfg.HealthCheckTemplateID, defaultHealthCheckTemplateID),
		login: albTarget{
			name:           "login",
			loadBalancerID: fallback(cfg.LoginLoadBalancerID, defaultLoginLoadBalancerID),
			listenerID:     fallback(cfg.LoginListenerID, defaultLoginListenerID),
		},
		pay: albTarget{
			name:           "pay",
			loadBalancerID: fallback(cfg.PayLoadBalancerID, defaultPayLoadBalancerID),
			listenerID:     fallback(cfg.PayListenerID, defaultPayListenerID),
		},
		ws: albTarget{
			name:           "ws",
			loadBalancerID: fallback(cfg.WSLoadBalancerID, defaultWSLoadBalancerID),
			listenerID:     fallback(cfg.WSListenerID, defaultWSListenerID),
		},
	}, nil
}

func (a *ALBSDKTeardown) SetupForRows(rows []model.NewServerRow, log LogFunc) error {
	for _, r := range rows {
		switch r.Kind {
		case "game":
			instanceID, err := a.findInstanceIDByPrivateIP(r.SelfPublicIp)
			if err != nil {
				return fmt.Errorf("game %d resolve ECS by ip %s: %w", r.ID, r.SelfPublicIp, err)
			}
			gamePort, err := parsePositiveInt32(strings.TrimSpace(r.Fields["PortForClient"]))
			if err != nil {
				return fmt.Errorf("game %d invalid PortForClient: %w", r.ID, err)
			}
			payPort, err := payPortFromGamePort(gamePort)
			if err != nil {
				return fmt.Errorf("game %d unsupported PortForClient %d: %w", r.ID, gamePort, err)
			}
			path := fmt.Sprintf("/s%d", r.ID)
			if err := a.ensurePath(path, instanceID, gamePort, a.login, serverGroupName(r.ID), log); err != nil {
				return err
			}
			if err := a.ensurePath(path, instanceID, payPort, a.pay, serverGroupPayName(r.ID), log); err != nil {
				return err
			}
		case "battle":
			copyRow, ok := pairedBattleCopy(rows, r.ID)
			if !ok {
				return fmt.Errorf("battle %d has no paired copy row", r.ID)
			}
			instanceID, err := a.findInstanceIDByPrivateIP(r.SelfPublicIp)
			if err != nil {
				return fmt.Errorf("battle %d resolve ECS by ip %s: %w", r.ID, r.SelfPublicIp, err)
			}
			if err := a.ensurePath(fmt.Sprintf("/s%d", copyRow.ID), instanceID, wsCopyBackendPort, a.ws, serverGroupName(copyRow.ID), log); err != nil {
				return err
			}
			if err := a.ensurePath(fmt.Sprintf("/s%d", r.ID), instanceID, wsBattleBackendPort, a.ws, serverGroupName(r.ID), log); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *ALBSDKTeardown) TeardownForRow(p model.DeleteServerParams, log LogFunc) error {
	path := fmt.Sprintf("/s%d", p.ID)
	switch p.Kind {
	case "game":
		return a.teardownPath(path, []albTarget{a.login, a.pay}, p, log)
	case "battle":
		return a.teardownPath(path, []albTarget{a.ws}, p, log)
	case "copy":
		if strings.TrimSpace(p.Fields["GlobalCenterWorldID"]) == "-1" {
			return a.teardownPath(path, []albTarget{a.ws}, p, log)
		}
		return nil
	default:
		return nil
	}
}

func (a *ALBSDKTeardown) ensurePath(path, instanceID string, backendPort int32, target albTarget, groupName string, log LogFunc) error {
	if target.loadBalancerID == "" || target.listenerID == "" {
		return fmt.Errorf("ALB(%s) listener config missing", target.name)
	}

	ruleID, _, err := a.findRuleAndServerGroup(target, path)
	if err != nil {
		return err
	}
	if ruleID != "" {
		log(fmt.Sprintf("ALB(%s) path %s already exists, skip setup", target.name, path))
		return nil
	}

	serverGroupID, err := a.createServerGroup(groupName)
	if err != nil {
		return fmt.Errorf("ALB(%s) create server group: %w", target.name, err)
	}
	cleanupGroup := true
	defer func() {
		if cleanupGroup {
			_ = a.deleteServerGroup(serverGroupID)
		}
	}()

	if err := a.applyHealthTemplate(serverGroupID); err != nil {
		return fmt.Errorf("ALB(%s) apply health template: %w", target.name, err)
	}
	if err := a.waitServerGroupAvailable(serverGroupID); err != nil {
		return fmt.Errorf("ALB(%s) wait server group available(after apply): %w", target.name, err)
	}
	if err := a.addServer(serverGroupID, instanceID, backendPort); err != nil {
		return fmt.Errorf("ALB(%s) add server: %w", target.name, err)
	}
	if err := a.waitServerGroupAvailable(serverGroupID); err != nil {
		return fmt.Errorf("ALB(%s) wait server group available(after add): %w", target.name, err)
	}
	priority, err := a.nextPriority(target)
	if err != nil {
		return fmt.Errorf("ALB(%s) get next priority: %w", target.name, err)
	}
	if err := a.createRule(target, path, priority, serverGroupID); err != nil {
		return fmt.Errorf("ALB(%s) create rule: %w", target.name, err)
	}

	cleanupGroup = false
	log(fmt.Sprintf("ALB(%s) setup ok: path=%s port=%d sg=%s", target.name, path, backendPort, serverGroupID))
	return nil
}

func (a *ALBSDKTeardown) createServerGroup(name string) (string, error) {
	req := (&alb.CreateServerGroupRequest{}).
		SetServerGroupName(name).
		SetVpcId(a.vpcID).
		SetDryRun(false).
		SetHealthCheckConfig((&alb.CreateServerGroupRequestHealthCheckConfig{}).SetHealthCheckEnabled(true)).
		SetStickySessionConfig((&alb.CreateServerGroupRequestStickySessionConfig{}).SetStickySessionEnabled(false))
	if a.resourceGroupID != "" {
		req.SetResourceGroupId(a.resourceGroupID)
	}
	resp, err := a.albClient.CreateServerGroup(req)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Body == nil || val(resp.Body.ServerGroupId) == "" {
		return "", fmt.Errorf("empty serverGroupId")
	}
	id := val(resp.Body.ServerGroupId)
	if err := a.waitServerGroupAvailable(id); err != nil {
		return "", fmt.Errorf("wait server group available(after create): %w", err)
	}
	return id, nil
}

func (a *ALBSDKTeardown) applyHealthTemplate(serverGroupID string) error {
	if strings.TrimSpace(a.healthCheckTemplateID) == "" {
		return nil
	}
	return withALBOpRetry(func() error {
		_, err := a.albClient.ApplyHealthCheckTemplateToServerGroup((&alb.ApplyHealthCheckTemplateToServerGroupRequest{}).
			SetServerGroupId(serverGroupID).
			SetHealthCheckTemplateId(a.healthCheckTemplateID).
			SetDryRun(false))
		return err
	})
}

func (a *ALBSDKTeardown) addServer(serverGroupID, instanceID string, port int32) error {
	return withALBOpRetry(func() error {
		_, err := a.albClient.AddServersToServerGroup((&alb.AddServersToServerGroupRequest{}).
			SetServerGroupId(serverGroupID).
			SetDryRun(false).
			SetServers([]*alb.AddServersToServerGroupRequestServers{
				(&alb.AddServersToServerGroupRequestServers{}).
					SetServerId(instanceID).
					SetServerType("Ecs").
					SetPort(port),
			}))
		return err
	})
}

func (a *ALBSDKTeardown) createRule(target albTarget, path string, priority int32, serverGroupID string) error {
	action := (&alb.CreateRuleRequestRuleActions{}).
		SetType("ForwardGroup").
		SetOrder(1).
		SetForwardGroupConfig((&alb.CreateRuleRequestRuleActionsForwardGroupConfig{}).
			SetServerGroupTuples([]*alb.CreateRuleRequestRuleActionsForwardGroupConfigServerGroupTuples{
				(&alb.CreateRuleRequestRuleActionsForwardGroupConfigServerGroupTuples{}).
					SetServerGroupId(serverGroupID),
			}))
	condition := (&alb.CreateRuleRequestRuleConditions{}).
		SetType("Path").
		SetPathConfig((&alb.CreateRuleRequestRuleConditionsPathConfig{}).SetValues([]*string{strp(path)}))

	return withALBOpRetry(func() error {
		_, err := a.albClient.CreateRule((&alb.CreateRuleRequest{}).
			SetListenerId(target.listenerID).
			SetPriority(priority).
			SetRuleName(strings.TrimPrefix(path, "/")).
			SetDryRun(false).
			SetRuleActions([]*alb.CreateRuleRequestRuleActions{action}).
			SetRuleConditions([]*alb.CreateRuleRequestRuleConditions{condition}))
		return err
	})
}

func (a *ALBSDKTeardown) nextPriority(target albTarget) (int32, error) {
	var maxPrio int32
	var nextToken string
	for {
		req := (&alb.ListRulesRequest{}).
			SetLoadBalancerIds([]*string{strp(target.loadBalancerID)}).
			SetListenerIds([]*string{strp(target.listenerID)}).
			SetMaxResults(50)
		if nextToken != "" {
			req.SetNextToken(nextToken)
		}
		resp, err := a.albClient.ListRules(req)
		if err != nil {
			return 0, err
		}
		if resp == nil || resp.Body == nil {
			return maxPrio + 1, nil
		}
		for _, r := range resp.Body.Rules {
			if r != nil && r.Priority != nil && *r.Priority > maxPrio {
				maxPrio = *r.Priority
			}
		}
		nextToken = val(resp.Body.NextToken)
		if nextToken == "" {
			return maxPrio + 1, nil
		}
	}
}

func (a *ALBSDKTeardown) findInstanceIDByPrivateIP(ip string) (string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("empty private ip")
	}

	privateIPs := fmt.Sprintf("[\"%s\"]", ip)
	var nextToken string
	for {
		req := (&ecs.DescribeInstancesRequest{}).
			SetRegionId(a.region).
			SetPrivateIpAddresses(privateIPs).
			SetMaxResults(100)
		if a.resourceGroupID != "" {
			req.SetResourceGroupId(a.resourceGroupID)
		}
		if nextToken != "" {
			req.SetNextToken(nextToken)
		}

		resp, err := a.ecsClient.DescribeInstances(req)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Body != nil && resp.Body.Instances != nil {
			for _, ins := range resp.Body.Instances.Instance {
				if ins == nil || val(ins.InstanceId) == "" {
					continue
				}
				if instanceHasPrivateIP(ins, ip) {
					return val(ins.InstanceId), nil
				}
			}
			nextToken = val(resp.Body.NextToken)
			if nextToken == "" {
				break
			}
			continue
		}
		break
	}
	return "", fmt.Errorf("instance not found by private ip: %s", ip)
}

func instanceHasPrivateIP(ins *ecs.DescribeInstancesResponseBodyInstancesInstance, want string) bool {
	if ins == nil || ins.VpcAttributes == nil || ins.VpcAttributes.PrivateIpAddress == nil {
		return false
	}
	for _, ip := range ins.VpcAttributes.PrivateIpAddress.IpAddress {
		if val(ip) == want {
			return true
		}
	}
	return false
}

func (a *ALBSDKTeardown) teardownPath(path string, targets []albTarget, p model.DeleteServerParams, log LogFunc) error {
	for _, t := range targets {
		if t.loadBalancerID == "" || t.listenerID == "" {
			log(fmt.Sprintf("ALB(%s) missing LB/Listener, skip %s", t.name, path))
			continue
		}
		expectedIDs, err := a.expectedServerGroupIDs(p, t)
		if err != nil {
			return err
		}
		ruleID, serverGroupID, err := a.findRuleAndServerGroup(t, path)
		if err != nil {
			return err
		}
		if ruleID == "" {
			log(fmt.Sprintf("ALB(%s) path %s not found, try server-group-name fallback", t.name, path))
		} else {
			if len(expectedIDs) > 0 && serverGroupID != "" && !containsString(expectedIDs, serverGroupID) {
				return fmt.Errorf("ALB(%s) rule %s path %s points to unexpected server group %s, expected one of %v, refuse delete",
					t.name, ruleID, path, serverGroupID, expectedIDs)
			}
			if err := a.deleteRule(ruleID); err != nil {
				return err
			}
			if err := a.waitRuleGone(t, path); err != nil {
				return err
			}
		}

		ids := []string{}
		if serverGroupID != "" {
			ids = append(ids, serverGroupID)
		}
		for _, fallbackName := range fallbackServerGroupNames(p, t) {
			fallbackID, err := a.findServerGroupIDByName(fallbackName)
			if err != nil {
				return err
			}
			if fallbackID != "" {
				ids = append(ids, fallbackID)
			}
		}
		ids = dedupeStrings(ids)
		if len(ids) == 0 {
			log(fmt.Sprintf("ALB(%s) no server group found for %s", t.name, path))
			continue
		}

		for _, sgID := range ids {
			inUse, err := a.serverGroupReferencedByAnyRule(sgID)
			if err != nil {
				return err
			}
			if inUse {
				log(fmt.Sprintf("ALB(%s) server group still referenced, skip delete: %s", t.name, sgID))
				continue
			}
			if err := a.cleanupServerGroup(sgID); err != nil {
				return err
			}
			log(fmt.Sprintf("ALB(%s) server group removed: %s", t.name, sgID))
		}
	}
	return nil
}

func (a *ALBSDKTeardown) cleanupServerGroup(serverGroupID string) error {
	servers, err := a.listServers(serverGroupID)
	if err != nil {
		return err
	}
	if len(servers) > 0 {
		if err := a.removeServers(serverGroupID, servers); err != nil {
			return err
		}
	}
	if err := a.waitServersCleared(serverGroupID); err != nil {
		return err
	}
	if err := a.waitServerGroupAvailable(serverGroupID); err != nil {
		return err
	}
	if err := a.deleteServerGroup(serverGroupID); err != nil {
		return err
	}
	if err := a.waitServerGroupDeleted(serverGroupID); err != nil {
		return err
	}
	return nil
}

func (a *ALBSDKTeardown) findServerGroupIDByName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	req := (&alb.ListServerGroupsRequest{}).
		SetServerGroupNames([]*string{strp(name)}).
		SetMaxResults(50)
	if a.resourceGroupID != "" {
		req.SetResourceGroupId(a.resourceGroupID)
	}
	if a.vpcID != "" {
		req.SetVpcId(a.vpcID)
	}

	var nextToken string
	for {
		if nextToken != "" {
			req.SetNextToken(nextToken)
		}
		resp, err := a.albClient.ListServerGroups(req)
		if err != nil {
			return "", fmt.Errorf("ListServerGroups(name=%s): %w", name, err)
		}
		if resp == nil || resp.Body == nil {
			return "", nil
		}
		for _, g := range resp.Body.ServerGroups {
			if g == nil {
				continue
			}
			if strings.TrimSpace(val(g.ServerGroupName)) == name {
				return strings.TrimSpace(val(g.ServerGroupId)), nil
			}
		}
		nextToken = val(resp.Body.NextToken)
		if nextToken == "" {
			return "", nil
		}
	}
}

func fallbackServerGroupNames(p model.DeleteServerParams, t albTarget) []string {
	switch p.Kind {
	case "game":
		switch t.name {
		case "login":
			return []string{serverGroupName(p.ID)}
		case "pay":
			return []string{serverGroupPayName(p.ID)}
		}
	case "battle", "copy":
		if t.name == "ws" {
			return []string{serverGroupName(p.ID)}
		}
	}
	return nil
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func containsString(ss []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, s := range ss {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}

func (a *ALBSDKTeardown) expectedServerGroupIDs(p model.DeleteServerParams, t albTarget) ([]string, error) {
	names := fallbackServerGroupNames(p, t)
	if len(names) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, err := a.findServerGroupIDByName(name)
		if err != nil {
			return nil, err
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids), nil
}

func (a *ALBSDKTeardown) serverGroupReferencedByAnyRule(serverGroupID string) (bool, error) {
	serverGroupID = strings.TrimSpace(serverGroupID)
	if serverGroupID == "" {
		return false, nil
	}
	loadBalancerIDs := dedupeStrings([]string{
		a.login.loadBalancerID,
		a.pay.loadBalancerID,
		a.ws.loadBalancerID,
	})
	for _, lbID := range loadBalancerIDs {
		if lbID == "" {
			continue
		}
		var nextToken string
		for {
			req := (&alb.ListRulesRequest{}).
				SetLoadBalancerIds([]*string{strp(lbID)}).
				SetMaxResults(50)
			if nextToken != "" {
				req.SetNextToken(nextToken)
			}
			resp, err := a.albClient.ListRules(req)
			if err != nil {
				return false, fmt.Errorf("ALB(lb=%s) ListRules for ref-check failed: %w", lbID, err)
			}
			if resp == nil || resp.Body == nil {
				break
			}
			for _, r := range resp.Body.Rules {
				if ruleReferencesServerGroup(r, serverGroupID) {
					return true, nil
				}
			}
			nextToken = val(resp.Body.NextToken)
			if nextToken == "" {
				break
			}
		}
	}
	return false, nil
}

func ruleReferencesServerGroup(r *alb.ListRulesResponseBodyRules, serverGroupID string) bool {
	if r == nil || strings.TrimSpace(serverGroupID) == "" {
		return false
	}
	for _, act := range r.RuleActions {
		if !strings.EqualFold(val(act.Type), "ForwardGroup") || act.ForwardGroupConfig == nil {
			continue
		}
		for _, t := range act.ForwardGroupConfig.ServerGroupTuples {
			if strings.TrimSpace(val(t.ServerGroupId)) == serverGroupID {
				return true
			}
		}
	}
	return false
}

func (a *ALBSDKTeardown) findRuleAndServerGroup(target albTarget, path string) (string, string, error) {
	var nextToken string
	for {
		req := (&alb.ListRulesRequest{}).
			SetLoadBalancerIds([]*string{strp(target.loadBalancerID)}).
			SetListenerIds([]*string{strp(target.listenerID)}).
			SetMaxResults(50)
		if nextToken != "" {
			req.SetNextToken(nextToken)
		}
		resp, err := a.albClient.ListRules(req)
		if err != nil {
			return "", "", fmt.Errorf("ALB(%s) ListRules failed: %w", target.name, err)
		}
		if resp == nil || resp.Body == nil {
			return "", "", nil
		}
		for _, r := range resp.Body.Rules {
			if !ruleHasPath(r, path) {
				continue
			}
			return val(r.RuleId), firstServerGroupID(r), nil
		}
		nextToken = val(resp.Body.NextToken)
		if nextToken == "" {
			return "", "", nil
		}
	}
}

func (a *ALBSDKTeardown) deleteRule(ruleID string) error {
	return withALBOpRetry(func() error {
		_, err := a.albClient.DeleteRule((&alb.DeleteRuleRequest{}).SetRuleId(ruleID).SetDryRun(false))
		if err != nil && !isNotFoundErr(err) {
			return fmt.Errorf("DeleteRule(%s): %w", ruleID, err)
		}
		return nil
	})
}

func (a *ALBSDKTeardown) listServers(serverGroupID string) ([]albServerRef, error) {
	var out []albServerRef
	var nextToken string
	for {
		req := (&alb.ListServerGroupServersRequest{}).
			SetServerGroupId(serverGroupID).
			SetMaxResults(50)
		if nextToken != "" {
			req.SetNextToken(nextToken)
		}
		resp, err := a.albClient.ListServerGroupServers(req)
		if err != nil {
			if isNotFoundErr(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("ListServerGroupServers(%s): %w", serverGroupID, err)
		}
		if resp == nil || resp.Body == nil {
			return out, nil
		}
		for _, s := range resp.Body.Servers {
			id := val(s.ServerId)
			if id == "" {
				continue
			}
			out = append(out, albServerRef{serverID: id, serverType: fallback(val(s.ServerType), "Ecs"), port: int32Val(s.Port)})
		}
		nextToken = val(resp.Body.NextToken)
		if nextToken == "" {
			return out, nil
		}
	}
}

func (a *ALBSDKTeardown) removeServers(serverGroupID string, servers []albServerRef) error {
	reqServers := make([]*alb.RemoveServersFromServerGroupRequestServers, 0, len(servers))
	for _, s := range servers {
		if s.serverID == "" || s.port <= 0 {
			continue
		}
		reqServers = append(reqServers, (&alb.RemoveServersFromServerGroupRequestServers{}).
			SetServerId(s.serverID).
			SetServerType(s.serverType).
			SetPort(s.port))
	}
	if len(reqServers) == 0 {
		return nil
	}
	return withALBOpRetry(func() error {
		_, err := a.albClient.RemoveServersFromServerGroup((&alb.RemoveServersFromServerGroupRequest{}).
			SetServerGroupId(serverGroupID).
			SetServers(reqServers).
			SetDryRun(false))
		return normalizeRemoveServersErr(serverGroupID, err)
	})
}

func (a *ALBSDKTeardown) deleteServerGroup(serverGroupID string) error {
	var lastErr error
	for i := 0; i < asyncPollMaxAttempts; i++ {
		_, err := a.albClient.DeleteServerGroup((&alb.DeleteServerGroupRequest{}).
			SetServerGroupId(serverGroupID).
			SetDryRun(false))
		if err == nil || isNotFoundErr(err) {
			return nil
		}
		lastErr = fmt.Errorf("DeleteServerGroup(%s): %w", serverGroupID, err)
		if !isRetryableALBErr(err) {
			return lastErr
		}
		if i < asyncPollMaxAttempts-1 {
			albRetrySleep(asyncPollDelay)
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("DeleteServerGroup(%s) timeout", serverGroupID)
}

func ruleHasPath(r *alb.ListRulesResponseBodyRules, path string) bool {
	if r == nil {
		return false
	}
	for _, c := range r.RuleConditions {
		if !strings.EqualFold(val(c.Type), "Path") || c.PathConfig == nil {
			continue
		}
		for _, v := range c.PathConfig.Values {
			if val(v) == path {
				return true
			}
		}
	}
	return false
}

func firstServerGroupID(r *alb.ListRulesResponseBodyRules) string {
	if r == nil {
		return ""
	}
	for _, a := range r.RuleActions {
		if !strings.EqualFold(val(a.Type), "ForwardGroup") || a.ForwardGroupConfig == nil {
			continue
		}
		for _, t := range a.ForwardGroupConfig.ServerGroupTuples {
			if id := val(t.ServerGroupId); id != "" {
				return id
			}
		}
	}
	return ""
}

func payPortFromGamePort(port int32) (int32, error) {
	switch port {
	case 3341:
		return 3345, nil
	case 3441:
		return 3445, nil
	case 3541:
		return 3545, nil
	case 3641:
		return 3645, nil
	default:
		return 0, fmt.Errorf("unsupported game port: %d", port)
	}
}

func parsePositiveInt32(s string) (int32, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid int: %q", s)
	}
	return int32(n), nil
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	keys := []string{"notfound", "not found", "invalidruleid.notfound", "invalidservergroupid.notfound", "resourcenotfound"}
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func isRetryableALBErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	keys := []string{
		"incorrectstatus.servergroup",
		"incorrectstatus.listener",
		"incorrectstatus.rule",
		"resourceinuse",
		"operationconflict",
		"throttling",
		"ratelimit",
		"servicebusy",
	}
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func withALBOpRetry(fn func() error) error {
	var err error
	for i := 1; i <= serverGroupReadyRetryMaxAttempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !isRetryableALBErr(err) || i == serverGroupReadyRetryMaxAttempts {
			return err
		}
		albRetrySleep(serverGroupReadyRetryDelay)
	}
	return err
}

func normalizeRemoveServersErr(serverGroupID string, err error) error {
	if err != nil && !isNotFoundErr(err) {
		return fmt.Errorf("RemoveServersFromServerGroup(%s): %w", serverGroupID, err)
	}
	return nil
}

func (a *ALBSDKTeardown) serverGroupStatus(serverGroupID string) (string, bool, error) {
	req := (&alb.ListServerGroupsRequest{}).
		SetServerGroupIds([]*string{strp(serverGroupID)}).
		SetMaxResults(10)
	if a.resourceGroupID != "" {
		req.SetResourceGroupId(a.resourceGroupID)
	}
	resp, err := a.albClient.ListServerGroups(req)
	if err != nil {
		if isNotFoundErr(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if resp == nil || resp.Body == nil {
		return "", false, nil
	}
	for _, g := range resp.Body.ServerGroups {
		if g != nil && val(g.ServerGroupId) == serverGroupID {
			return strings.TrimSpace(val(g.ServerGroupStatus)), true, nil
		}
	}
	return "", false, nil
}

func (a *ALBSDKTeardown) waitServerGroupAvailable(serverGroupID string) error {
	for i := 0; i < asyncPollMaxAttempts; i++ {
		status, exists, err := a.serverGroupStatus(serverGroupID)
		if err == nil && exists && strings.EqualFold(status, "Available") {
			return nil
		}
		if err != nil && !isRetryableALBErr(err) {
			return fmt.Errorf("wait server group %s available: %w", serverGroupID, err)
		}
		if i < asyncPollMaxAttempts-1 {
			albRetrySleep(asyncPollDelay)
		}
	}
	return fmt.Errorf("wait server group %s available timeout", serverGroupID)
}

func (a *ALBSDKTeardown) waitServerGroupDeleted(serverGroupID string) error {
	for i := 0; i < asyncPollMaxAttempts; i++ {
		_, exists, err := a.serverGroupStatus(serverGroupID)
		if err == nil && !exists {
			return nil
		}
		if err != nil && !isRetryableALBErr(err) && !isNotFoundErr(err) {
			return fmt.Errorf("wait server group %s deleted: %w", serverGroupID, err)
		}
		if i < asyncPollMaxAttempts-1 {
			albRetrySleep(asyncPollDelay)
		}
	}
	return fmt.Errorf("wait server group %s deleted timeout", serverGroupID)
}

func (a *ALBSDKTeardown) waitRuleGone(target albTarget, path string) error {
	for i := 0; i < asyncPollMaxAttempts; i++ {
		ruleID, _, err := a.findRuleAndServerGroup(target, path)
		if err == nil && ruleID == "" {
			return nil
		}
		if err != nil && !isRetryableALBErr(err) {
			return fmt.Errorf("wait ALB(%s) rule %s gone: %w", target.name, path, err)
		}
		if i < asyncPollMaxAttempts-1 {
			albRetrySleep(asyncPollDelay)
		}
	}
	return fmt.Errorf("wait ALB(%s) rule %s gone timeout", target.name, path)
}

func (a *ALBSDKTeardown) waitServersCleared(serverGroupID string) error {
	for i := 0; i < asyncPollMaxAttempts; i++ {
		servers, err := a.listServers(serverGroupID)
		if err == nil && len(servers) == 0 {
			return nil
		}
		if err != nil && !isRetryableALBErr(err) && !isNotFoundErr(err) {
			return fmt.Errorf("wait server group %s servers cleared: %w", serverGroupID, err)
		}
		if i < asyncPollMaxAttempts-1 {
			albRetrySleep(asyncPollDelay)
		}
	}
	return fmt.Errorf("wait server group %s servers cleared timeout", serverGroupID)
}

func fallback(v, d string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return d
	}
	return v
}

func val(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func int32Val(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func serverGroupName(id int) string {
	return fmt.Sprintf("%s%d", serverGroupNamePrefix, id)
}

func serverGroupPayName(id int) string {
	return fmt.Sprintf("%s%d%s", serverGroupNamePrefix, id, serverGroupPaySuffix)
}

func strp(s string) *string { return &s }
