package executor

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	alb "github.com/alibabacloud-go/alb-20200616/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"

	"gongdan/internal/model"
)

type fakeALBClient struct {
	listRulesResp      *alb.ListRulesResponse
	serverGroupByName  map[string]string
	createServerGroups int
	addPorts           []int32
	createdRules       []string
	deleteRuleCalls    int
	deletedRuleIDs     []string
	applyErrors        []error
	applyCalls         int
	addErrors          []error
	addCalls           int
	deleteSGErrors     []error
	deleteSGCalls      int
	deletedSG          map[string]bool
}

func (f *fakeALBClient) ListRules(_ *alb.ListRulesRequest) (*alb.ListRulesResponse, error) {
	if f.listRulesResp != nil {
		return f.listRulesResp, nil
	}
	return &alb.ListRulesResponse{Body: &alb.ListRulesResponseBody{Rules: []*alb.ListRulesResponseBodyRules{}}}, nil
}

func (f *fakeALBClient) ListServerGroups(req *alb.ListServerGroupsRequest) (*alb.ListServerGroupsResponse, error) {
	if req == nil {
		return &alb.ListServerGroupsResponse{Body: &alb.ListServerGroupsResponseBody{ServerGroups: []*alb.ListServerGroupsResponseBodyServerGroups{}}}, nil
	}

	groups := make([]*alb.ListServerGroupsResponseBodyServerGroups, 0, len(req.ServerGroupIds)+len(req.ServerGroupNames))
	appendGroup := func(id, name string) {
		if id == "" {
			return
		}
		if f.deletedSG != nil && f.deletedSG[id] {
			return
		}
		groups = append(groups, (&alb.ListServerGroupsResponseBodyServerGroups{}).
			SetServerGroupId(id).
			SetServerGroupName(name).
			SetServerGroupStatus("Available"))
	}

	for _, idp := range req.ServerGroupIds {
		if idp == nil {
			continue
		}
		appendGroup(*idp, "")
	}
	for _, namep := range req.ServerGroupNames {
		if namep == nil || f.serverGroupByName == nil {
			continue
		}
		name := *namep
		appendGroup(f.serverGroupByName[name], name)
	}
	return &alb.ListServerGroupsResponse{Body: &alb.ListServerGroupsResponseBody{ServerGroups: groups}}, nil
}

func (f *fakeALBClient) DeleteRule(req *alb.DeleteRuleRequest) (*alb.DeleteRuleResponse, error) {
	f.deleteRuleCalls++
	if req != nil && req.RuleId != nil {
		f.deletedRuleIDs = append(f.deletedRuleIDs, *req.RuleId)
	}
	return &alb.DeleteRuleResponse{}, nil
}

func (f *fakeALBClient) ListServerGroupServers(_ *alb.ListServerGroupServersRequest) (*alb.ListServerGroupServersResponse, error) {
	return &alb.ListServerGroupServersResponse{Body: &alb.ListServerGroupServersResponseBody{Servers: []*alb.ListServerGroupServersResponseBodyServers{}}}, nil
}

func (f *fakeALBClient) RemoveServersFromServerGroup(_ *alb.RemoveServersFromServerGroupRequest) (*alb.RemoveServersFromServerGroupResponse, error) {
	return &alb.RemoveServersFromServerGroupResponse{}, nil
}

func (f *fakeALBClient) DeleteServerGroup(req *alb.DeleteServerGroupRequest) (*alb.DeleteServerGroupResponse, error) {
	f.deleteSGCalls++
	if len(f.deleteSGErrors) > 0 {
		err := f.deleteSGErrors[0]
		f.deleteSGErrors = f.deleteSGErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	if f.deletedSG == nil {
		f.deletedSG = map[string]bool{}
	}
	if req != nil && req.ServerGroupId != nil {
		f.deletedSG[*req.ServerGroupId] = true
	}
	return &alb.DeleteServerGroupResponse{}, nil
}

func (f *fakeALBClient) CreateServerGroup(_ *alb.CreateServerGroupRequest) (*alb.CreateServerGroupResponse, error) {
	f.createServerGroups++
	sgID := "sg-" + strconvI(f.createServerGroups)
	return &alb.CreateServerGroupResponse{Body: &alb.CreateServerGroupResponseBody{ServerGroupId: strp(sgID)}}, nil
}

func (f *fakeALBClient) ApplyHealthCheckTemplateToServerGroup(_ *alb.ApplyHealthCheckTemplateToServerGroupRequest) (*alb.ApplyHealthCheckTemplateToServerGroupResponse, error) {
	f.applyCalls++
	if len(f.applyErrors) > 0 {
		err := f.applyErrors[0]
		f.applyErrors = f.applyErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &alb.ApplyHealthCheckTemplateToServerGroupResponse{}, nil
}

func (f *fakeALBClient) AddServersToServerGroup(req *alb.AddServersToServerGroupRequest) (*alb.AddServersToServerGroupResponse, error) {
	f.addCalls++
	if len(f.addErrors) > 0 {
		err := f.addErrors[0]
		f.addErrors = f.addErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	if req != nil && len(req.Servers) > 0 && req.Servers[0] != nil && req.Servers[0].Port != nil {
		f.addPorts = append(f.addPorts, *req.Servers[0].Port)
	}
	return &alb.AddServersToServerGroupResponse{}, nil
}

func (f *fakeALBClient) CreateRule(req *alb.CreateRuleRequest) (*alb.CreateRuleResponse, error) {
	listener := ""
	if req != nil && req.ListenerId != nil {
		listener = *req.ListenerId
	}
	path := ""
	if req != nil && len(req.RuleConditions) > 0 && req.RuleConditions[0] != nil &&
		req.RuleConditions[0].PathConfig != nil && len(req.RuleConditions[0].PathConfig.Values) > 0 && req.RuleConditions[0].PathConfig.Values[0] != nil {
		path = *req.RuleConditions[0].PathConfig.Values[0]
	}
	f.createdRules = append(f.createdRules, listener+":"+path)
	return &alb.CreateRuleResponse{}, nil
}

type fakeECSClient struct {
	ip         string
	instanceID string
	calls      int
}

func (f *fakeECSClient) DescribeInstances(_ *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	f.calls++
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{
		Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
			{
				InstanceId: strp(f.instanceID),
				VpcAttributes: &ecs.DescribeInstancesResponseBodyInstancesInstanceVpcAttributes{
					PrivateIpAddress: &ecs.DescribeInstancesResponseBodyInstancesInstanceVpcAttributesPrivateIpAddress{IpAddress: []*string{strp(f.ip)}},
				},
			},
		}},
	}}, nil
}

func TestALBSDKSetupGame(t *testing.T) {
	albClient := &fakeALBClient{}
	ecsClient := &fakeECSClient{ip: "10.0.0.1", instanceID: "i-1"}
	m := &ALBSDKTeardown{
		albClient:             albClient,
		ecsClient:             ecsClient,
		region:                "cn-hangzhou",
		resourceGroupID:       "rg-x",
		vpcID:                 "vpc-x",
		healthCheckTemplateID: "hct-x",
		login:                 albTarget{name: "login", loadBalancerID: "lb1", listenerID: "lsn1"},
		pay:                   albTarget{name: "pay", loadBalancerID: "lb2", listenerID: "lsn2"},
		ws:                    albTarget{name: "ws", loadBalancerID: "lb3", listenerID: "lsn3"},
	}
	rows := []model.NewServerRow{{
		Kind:         "game",
		ID:           10002,
		SelfPublicIp: "10.0.0.1",
		Fields:       map[string]string{"PortForClient": "3441"},
	}}

	if err := m.SetupForRows(rows, func(string) {}); err != nil {
		t.Fatalf("SetupForRows(game) failed: %v", err)
	}
	if ecsClient.calls != 1 {
		t.Fatalf("ecs lookup calls want=1 got=%d", ecsClient.calls)
	}
	if albClient.createServerGroups != 2 {
		t.Fatalf("create server group count want=2 got=%d", albClient.createServerGroups)
	}
	ports := append([]int32{}, albClient.addPorts...)
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	if !reflect.DeepEqual(ports, []int32{3441, 3445}) {
		t.Fatalf("backend ports mismatch want=[3441 3445] got=%v", ports)
	}
	rules := append([]string{}, albClient.createdRules...)
	sort.Strings(rules)
	wantRules := []string{"lsn1:/s10002", "lsn2:/s10002"}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Fatalf("created rules mismatch want=%v got=%v", wantRules, rules)
	}
}

func TestALBSDKSetupBattlePair(t *testing.T) {
	albClient := &fakeALBClient{}
	ecsClient := &fakeECSClient{ip: "10.1.0.2", instanceID: "i-2"}
	m := &ALBSDKTeardown{
		albClient:             albClient,
		ecsClient:             ecsClient,
		region:                "cn-hangzhou",
		resourceGroupID:       "rg-x",
		vpcID:                 "vpc-x",
		healthCheckTemplateID: "hct-x",
		login:                 albTarget{name: "login", loadBalancerID: "lb1", listenerID: "lsn1"},
		pay:                   albTarget{name: "pay", loadBalancerID: "lb2", listenerID: "lsn2"},
		ws:                    albTarget{name: "ws", loadBalancerID: "lb3", listenerID: "lsn3"},
	}
	rows := []model.NewServerRow{
		{Kind: "battle", ID: 12802, SelfPublicIp: "10.1.0.2", Fields: map[string]string{"BattleWorldID": "12802", "GlobalCenterWorldID": "-1"}},
		{Kind: "copy", ID: 12002, SelfPublicIp: "10.1.0.2", Fields: map[string]string{"BattleWorldID": "12802", "GlobalCenterWorldID": "-1"}},
	}

	if err := m.SetupForRows(rows, func(string) {}); err != nil {
		t.Fatalf("SetupForRows(battle) failed: %v", err)
	}
	ports := append([]int32{}, albClient.addPorts...)
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	if !reflect.DeepEqual(ports, []int32{3841, 3941}) {
		t.Fatalf("backend ports mismatch want=[3841 3941] got=%v", ports)
	}
	rules := append([]string{}, albClient.createdRules...)
	sort.Strings(rules)
	wantRules := []string{"lsn3:/s12002", "lsn3:/s12802"}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Fatalf("created rules mismatch want=%v got=%v", wantRules, rules)
	}
}

func TestALBSDKSetupSkipWhenRuleExists(t *testing.T) {
	p := int32(99)
	albClient := &fakeALBClient{listRulesResp: &alb.ListRulesResponse{Body: &alb.ListRulesResponseBody{Rules: []*alb.ListRulesResponseBodyRules{
		{
			RuleId:   strp("r-1"),
			Priority: &p,
			RuleConditions: []*alb.ListRulesResponseBodyRulesRuleConditions{
				{Type: strp("Path"), PathConfig: &alb.ListRulesResponseBodyRulesRuleConditionsPathConfig{Values: []*string{strp("/s10002")}}},
			},
			RuleActions: []*alb.ListRulesResponseBodyRulesRuleActions{
				{Type: strp("ForwardGroup"), ForwardGroupConfig: &alb.ListRulesResponseBodyRulesRuleActionsForwardGroupConfig{ServerGroupTuples: []*alb.ListRulesResponseBodyRulesRuleActionsForwardGroupConfigServerGroupTuples{{ServerGroupId: strp("sg-old")}}}},
			},
		},
	}}}}
	ecsClient := &fakeECSClient{ip: "10.0.0.1", instanceID: "i-1"}
	m := &ALBSDKTeardown{
		albClient:             albClient,
		ecsClient:             ecsClient,
		region:                "cn-hangzhou",
		resourceGroupID:       "rg-x",
		vpcID:                 "vpc-x",
		healthCheckTemplateID: "hct-x",
		login:                 albTarget{name: "login", loadBalancerID: "lb1", listenerID: "lsn1"},
		pay:                   albTarget{name: "pay", loadBalancerID: "lb2", listenerID: "lsn2"},
		ws:                    albTarget{name: "ws", loadBalancerID: "lb3", listenerID: "lsn3"},
	}

	rows := []model.NewServerRow{{Kind: "game", ID: 10002, SelfPublicIp: "10.0.0.1", Fields: map[string]string{"PortForClient": "3441"}}}
	if err := m.SetupForRows(rows, func(string) {}); err != nil {
		t.Fatalf("SetupForRows(skip) failed: %v", err)
	}
	if albClient.createServerGroups != 0 {
		t.Fatalf("expected no server-group creation, got=%d", albClient.createServerGroups)
	}
	if len(albClient.createdRules) != 0 {
		t.Fatalf("expected no rule creation, got=%v", albClient.createdRules)
	}
}

func TestALBSDKTeardownFallbackDeleteByNameWhenRuleMissing(t *testing.T) {
	id := 50002
	albClient := &fakeALBClient{
		serverGroupByName: map[string]string{
			serverGroupName(id): "sg-login",
		},
	}
	m := &ALBSDKTeardown{
		albClient: albClient,
		login:     albTarget{name: "login", loadBalancerID: "lb1", listenerID: "lsn1"},
		pay:       albTarget{name: "pay", loadBalancerID: "lb2", listenerID: "lsn2"},
	}

	err := m.TeardownForRow(model.DeleteServerParams{
		ID:     id,
		Kind:   "game",
		Fields: map[string]string{},
	}, func(string) {})
	if err != nil {
		t.Fatalf("TeardownForRow(fallback by name) failed: %v", err)
	}
	if !albClient.deletedSG["sg-login"] {
		t.Fatalf("expected fallback server group sg-login to be deleted")
	}
}

func TestALBSDKTeardownFallbackSkipWhenServerGroupStillReferenced(t *testing.T) {
	id := 50002
	p := int32(10)
	albClient := &fakeALBClient{
		serverGroupByName: map[string]string{
			serverGroupName(id): "sg-login",
		},
		listRulesResp: &alb.ListRulesResponse{Body: &alb.ListRulesResponseBody{Rules: []*alb.ListRulesResponseBodyRules{
			{
				RuleId:   strp("r-other"),
				Priority: &p,
				RuleConditions: []*alb.ListRulesResponseBodyRulesRuleConditions{
					{Type: strp("Path"), PathConfig: &alb.ListRulesResponseBodyRulesRuleConditionsPathConfig{Values: []*string{strp("/s-other")}}},
				},
				RuleActions: []*alb.ListRulesResponseBodyRulesRuleActions{
					{Type: strp("ForwardGroup"), ForwardGroupConfig: &alb.ListRulesResponseBodyRulesRuleActionsForwardGroupConfig{
						ServerGroupTuples: []*alb.ListRulesResponseBodyRulesRuleActionsForwardGroupConfigServerGroupTuples{
							{ServerGroupId: strp("sg-login")},
						},
					}},
				},
			},
		}}},
	}
	m := &ALBSDKTeardown{
		albClient: albClient,
		login:     albTarget{name: "login", loadBalancerID: "lb1", listenerID: "lsn1"},
		pay:       albTarget{name: "pay", loadBalancerID: "lb2", listenerID: "lsn2"},
	}

	err := m.TeardownForRow(model.DeleteServerParams{
		ID:     id,
		Kind:   "game",
		Fields: map[string]string{},
	}, func(string) {})
	if err != nil {
		t.Fatalf("TeardownForRow(skip referenced fallback) failed: %v", err)
	}
	if albClient.deleteSGCalls != 0 {
		t.Fatalf("referenced server group should not be deleted, calls=%d", albClient.deleteSGCalls)
	}
}

func TestALBSDKTeardownRefuseDeleteRuleWhenServerGroupUnexpected(t *testing.T) {
	id := 50002
	p := int32(10)
	albClient := &fakeALBClient{
		serverGroupByName: map[string]string{
			serverGroupName(id): "sg-expected",
		},
		listRulesResp: &alb.ListRulesResponse{Body: &alb.ListRulesResponseBody{Rules: []*alb.ListRulesResponseBodyRules{
			{
				RuleId:   strp("r-login"),
				Priority: &p,
				RuleConditions: []*alb.ListRulesResponseBodyRulesRuleConditions{
					{Type: strp("Path"), PathConfig: &alb.ListRulesResponseBodyRulesRuleConditionsPathConfig{Values: []*string{strp("/s50002")}}},
				},
				RuleActions: []*alb.ListRulesResponseBodyRulesRuleActions{
					{Type: strp("ForwardGroup"), ForwardGroupConfig: &alb.ListRulesResponseBodyRulesRuleActionsForwardGroupConfig{
						ServerGroupTuples: []*alb.ListRulesResponseBodyRulesRuleActionsForwardGroupConfigServerGroupTuples{
							{ServerGroupId: strp("sg-other")},
						},
					}},
				},
			},
		}}},
	}
	m := &ALBSDKTeardown{
		albClient: albClient,
		login:     albTarget{name: "login", loadBalancerID: "lb1", listenerID: "lsn1"},
	}

	err := m.TeardownForRow(model.DeleteServerParams{
		ID:     id,
		Kind:   "game",
		Fields: map[string]string{},
	}, func(string) {})
	if err == nil {
		t.Fatal("expected unexpected-server-group guard to refuse rule deletion")
	}
	if albClient.deleteRuleCalls != 0 {
		t.Fatalf("rule should not be deleted when sg mismatched, deleteRuleCalls=%d", albClient.deleteRuleCalls)
	}
}

func TestApplyHealthTemplateRetriesOnCreatingStatus(t *testing.T) {
	oldSleep := albRetrySleep
	albRetrySleep = func(time.Duration) {}
	defer func() { albRetrySleep = oldSleep }()

	albClient := &fakeALBClient{
		applyErrors: []error{
			errors.New("SDKError: Code: IncorrectStatus.ServerGroup, Message: status is Creating"),
			nil,
		},
	}
	m := &ALBSDKTeardown{albClient: albClient, healthCheckTemplateID: "hct-x"}
	if err := m.applyHealthTemplate("sg-1"); err != nil {
		t.Fatalf("applyHealthTemplate retry failed: %v", err)
	}
	if albClient.applyCalls != 2 {
		t.Fatalf("apply calls want=2 got=%d", albClient.applyCalls)
	}
}

func TestAddServerRetriesOnCreatingStatus(t *testing.T) {
	oldSleep := albRetrySleep
	albRetrySleep = func(time.Duration) {}
	defer func() { albRetrySleep = oldSleep }()

	albClient := &fakeALBClient{
		addErrors: []error{
			errors.New("SDKError: Code: IncorrectStatus.ServerGroup, Message: status is Creating"),
			nil,
		},
	}
	m := &ALBSDKTeardown{albClient: albClient}
	if err := m.addServer("sg-1", "i-1", 3441); err != nil {
		t.Fatalf("addServer retry failed: %v", err)
	}
	if albClient.addCalls != 2 {
		t.Fatalf("add calls want=2 got=%d", albClient.addCalls)
	}
}

func TestDeleteServerGroupRetriesOnResourceInUse(t *testing.T) {
	oldSleep := albRetrySleep
	albRetrySleep = func(time.Duration) {}
	defer func() { albRetrySleep = oldSleep }()

	albClient := &fakeALBClient{
		deleteSGErrors: []error{
			errors.New("SDKError: Code: ResourceInUse.ServerGroup, Message: in use"),
			nil,
		},
	}
	m := &ALBSDKTeardown{albClient: albClient}
	if err := m.deleteServerGroup("sg-1"); err != nil {
		t.Fatalf("deleteServerGroup retry failed: %v", err)
	}
	if albClient.deleteSGCalls != 2 {
		t.Fatalf("delete server group calls want=2 got=%d", albClient.deleteSGCalls)
	}
}

func strconvI(n int) string {
	if n == 0 {
		return "0"
	}
	b := make([]byte, 0, 12)
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
