package gameserver

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPlanCreateGamePublicUrlByType(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName: "外网测试", WorldName: "U1",
		Slot:        GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp: "game-rds", AddPublicUrl: true,
		BattleEnabled: true, NewBattleMachine: MachineFrom("10.1.0.2", "9.9.9.2"), BattleMySqlIp: "b-rds",
	})
	if err != nil {
		t.Fatalf("PlanCreateGameServer: %v", err)
	}
	for _, r := range plan.Rows {
		want := "wss://ws-tw.tlh5.qinxiand.com/s" + strconv.Itoa(r.ID) // 战斗/副本
		if r.Kind == "game" {
			want = "wss://gs-tw.tlh5.qinxiand.com/s" + strconv.Itoa(r.ID) // 游戏服 gs-tw
		}
		if got := r.Fields["RealSelfPublicUrl"]; got != want {
			t.Errorf("%s(%d) RealSelfPublicUrl=%q want %q", r.Kind, r.ID, got, want)
		}
	}
}

func TestPlanCreateGamePublicUrlAsIP(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName: "IP测试", WorldName: "U2",
		Slot:        GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp: "game-rds", // AddPublicUrl 默认 false
	})
	if err != nil {
		t.Fatalf("PlanCreateGameServer: %v", err)
	}
	if g := plan.Rows[0].Fields["RealSelfPublicUrl"]; g != "1.1.1.1" {
		t.Errorf("不加外网地址应填外网IP, got %q", g)
	}
}

func TestPlanCreateGameManualSlotPicksFreeBand(t *testing.T) {
	// 10.0.0.1 已被 10001 占用 3300 档,手填该IP应自动取最小空闲档 3400 → PortForClient 3441
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName: "手填测试服", WorldName: "M1",
		Slot:        GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 0},
		GameMySqlIp: "game-rds", BattleEnabled: true, ExistingBattleID: 12801,
	})
	if err != nil {
		t.Fatalf("PlanCreateGameServer: %v", err)
	}
	if g := plan.Rows[0].Fields; g["PortForClient"] != "3441" || g["SelfPublicIp"] != "10.0.0.1" {
		t.Errorf("手填IP应取空闲档3400→3441, got %+v", g)
	}
}

func TestPlanCreateGameManualSlotFullErrors(t *testing.T) {
	rows := createFixture()
	for i, pc := range []string{"3341", "3441", "3541"} { // 把 10.9.9.9 三档占满
		rows = append(rows, map[string]string{"Id": "1190" + string(rune('1'+i)),
			"WorldType": "0", "SelfPublicIp": "10.9.9.9", "RealSelfPublicIp": "1.1.1.9", "PortForClient": pc})
	}
	_, err := PlanCreateGameServer(rows, CreateGameParams{
		ServerName: "满档", WorldName: "F9",
		Slot:        GameSlot{SelfPublicIp: "10.9.9.9", PortBase: 0},
		GameMySqlIp: "g", BattleEnabled: true, ExistingBattleID: 12801,
	})
	if err == nil || !strings.Contains(err.Error(), "端口档") {
		t.Fatalf("三档全占应报错, got %v", err)
	}
}

func TestPlanCreateGameNoBattle(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName: "无战斗服", WorldName: "NB1",
		Slot:        GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp: "game-rds", // BattleEnabled 默认 false
	})
	if err != nil {
		t.Fatalf("PlanCreateGameServer: %v", err)
	}
	if len(plan.Rows) != 1 || plan.Rows[0].Kind != "game" {
		t.Fatalf("不挂战斗服应只 1 行游戏服, got %+v", plan.Rows)
	}
	if g := plan.Rows[0].Fields["BattleWorldID"]; g != "-1" {
		t.Errorf("不挂战斗服 BattleWorldID 应=-1, got %s", g)
	}
}

// 新发号规则:每类型按自己的 WorldType max+1(中心服=WT4 max+1=298,不再和战斗服共池=12802)。
func TestPlanCreateGamePerTypeAllocation(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName: "洛阳100服", WorldName: "Y100",
		Slot:             GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp:      "g-rds",
		BattleEnabled:    true,
		NewBattleMachine: MachineFrom("10.1.0.5", "9.9.9.5"), BattleMySqlIp: "b-rds",
		NewCenterMachine: MachineFrom("10.1.0.6", "9.9.9.6"), CenterMySqlIp: "c-rds",
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	got := map[string]int{}
	for _, r := range plan.Rows {
		got[r.Kind] = r.ID
	}
	if got["game"] != 10002 {
		t.Errorf("game=%d, want 10002(WT0 max+1)", got["game"])
	}
	if got["battle"] != 12802 {
		t.Errorf("battle=%d, want 12802(WT2 max+1)", got["battle"])
	}
	if got["center"] != 298 {
		t.Errorf("center=%d, want 298(WT4 max+1,不再和战斗共池)", got["center"])
	}
}

// 手填起始号覆盖 max+1:用 ManualBattleID 强制指定战斗服号。
func TestPlanCreateGameManualOverride(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName: "洛阳100服", WorldName: "Y100",
		Slot:             GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp:      "g-rds",
		BattleEnabled:    true,
		NewBattleMachine: MachineFrom("10.1.0.5", "9.9.9.5"), BattleMySqlIp: "b-rds",
		ManualBattleID: 13000,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	got := map[string]int{}
	for _, r := range plan.Rows {
		got[r.Kind] = r.ID
	}
	if got["battle"] != 13000 {
		t.Errorf("battle=%d, want 13000(手填起始号优先)", got["battle"])
	}
}

func createFixture() []map[string]string {
	return []map[string]string{
		{"Id": "10001", "WorldType": "0", "SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1",
			"PortForClient": "3341", "BattleWorldID": "12801",
			"WorldName": "LY1", "ServerShowName": "洛阳1服", "WoRegion": "洛阳"},
		{"Id": "12801", "WorldType": "2", "SelfPublicIp": "10.1.0.1", "RealSelfPublicIp": "9.9.9.1",
			"PortForClient": "3841"},
		{"Id": "12001", "WorldType": "3", "SelfPublicIp": "10.1.0.1", "RealSelfPublicIp": "9.9.9.1",
			"PortForClient": "3941"},
		{"Id": "12810", "WorldType": "-1", "SelfPublicIp": "10.1.0.2", "RealSelfPublicIp": "9.9.9.2"},
		{"Id": "297", "WorldType": "4", "SelfPublicIp": "192.168.11.160", "RealSelfPublicIp": "8.8.8.1",
			"PortForClient": "3541", "WorldName": "worldcenter297"},
	}
}

func TestAvailableGameSlots(t *testing.T) {
	slots := AvailableGameSlots(createFixture())
	var found340, found350 bool
	for _, s := range slots {
		if s.SelfPublicIp == "10.0.0.1" && s.PortBase == 3400 {
			found340 = true
		}
		if s.SelfPublicIp == "10.0.0.1" && s.PortBase == 3500 {
			found350 = true
		}
		if s.SelfPublicIp == "10.0.0.1" && s.PortBase == 3300 {
			t.Errorf("3300 已被占用,不应出现在可用槽")
		}
	}
	if !found340 || !found350 {
		t.Errorf("应含 GA 的 3400/3500 空闲档, slots=%+v", slots)
	}
}

// 低 Id 的在用游戏服(WorldType=0 但 Id<10000)也应占端口档:
// 同机上 235 占 3300、10000 占 3500,空闲只剩 3400,3300 不应再被列为可落点。
func TestAvailableGameSlotsLowIdGameServerOccupies(t *testing.T) {
	rows := []map[string]string{
		{"Id": "235", "WorldType": "0", "SelfPublicIp": "192.168.11.166", "RealSelfPublicIp": "9.9.9.9", "PortForClient": "3341"},
		{"Id": "10000", "WorldType": "0", "SelfPublicIp": "192.168.11.166", "RealSelfPublicIp": "9.9.9.9", "PortForClient": "3541"},
	}
	slots := AvailableGameSlots(rows)
	var bases []int
	for _, s := range slots {
		if s.SelfPublicIp != "192.168.11.166" {
			t.Fatalf("意外机器 %+v", s)
		}
		if s.PortBase == 3300 {
			t.Errorf("3300 已被低 Id 游戏服 235 占用,不应出现在可用槽")
		}
		bases = append(bases, s.PortBase)
	}
	if !reflect.DeepEqual(bases, []int{3400}) {
		t.Errorf("应只剩 3400 空闲档, got %v", bases)
	}
}

func TestBattleServersWithCapacity(t *testing.T) {
	opts := BattleServersWithCapacity(createFixture())
	if len(opts) != 1 || opts[0].ID != 12801 || opts[0].AttachedGames != 1 {
		t.Fatalf("应有 1 个有容量战斗服(12801,挂1), got %+v", opts)
	}
}

func TestBattleServersWithCapacityExcludesFull(t *testing.T) {
	rows := createFixture()
	for i, id := range []string{"10002", "10003", "10004"} {
		rows = append(rows, map[string]string{"Id": id, "WorldType": "0",
			"SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1",
			"PortForClient": "34" + string(rune('4'+i)) + "1", "BattleWorldID": "12801"})
	}
	opts := BattleServersWithCapacity(rows)
	for _, o := range opts {
		if o.ID == 12801 {
			t.Errorf("12801 已满 4 个游戏服,不应有容量")
		}
	}
}

func TestFreeBattleMachinesExported(t *testing.T) {
	ms := FreeBattleMachinesFor(createFixture())
	if len(ms) != 1 || ms[0].SelfIp() != "10.1.0.2" {
		t.Fatalf("空闲战斗机应为 BY(10.1.0.2), got %+v", ms)
	}
}

func TestCenterServers(t *testing.T) {
	rows := []map[string]string{
		// 中心服 297,挂多个 WT3 中心副本(GlobalCenterWorldID=297);乱序,断言按 Id 升序
		{"Id": "297", "WorldType": "4", "GlobalCenterWorldID": "297", "SelfPublicIp": "192.168.11.160"},
		{"Id": "12051", "WorldType": "3", "GlobalCenterWorldID": "297", "BattleWorldID": "-1", "SelfPublicIp": "192.168.11.160"},
		{"Id": "12050", "WorldType": "3", "GlobalCenterWorldID": "297", "BattleWorldID": "-1", "SelfPublicIp": "192.168.11.160"},
		// 干扰项:普通战斗副本(GlobalCenterWorldID=-1)不应被当成中心副本
		{"Id": "12001", "WorldType": "3", "GlobalCenterWorldID": "-1", "BattleWorldID": "12801", "SelfPublicIp": "10.1.0.1"},
		// 中心服 397,无对应 WT3 -> CopyIDs 空
		{"Id": "397", "WorldType": "4", "GlobalCenterWorldID": "397", "SelfPublicIp": "192.168.11.161"},
	}
	cs := CenterServers(rows)
	if len(cs) != 2 {
		t.Fatalf("应有 2 个中心服, got %+v", cs)
	}
	if cs[0].ID != 297 || !reflect.DeepEqual(cs[0].CopyIDs, []int{12050, 12051}) {
		t.Errorf("中心服 297 应挂副本 [12050 12051], got %+v", cs[0])
	}
	if cs[1].ID != 397 || len(cs[1].CopyIDs) != 0 {
		t.Errorf("中心服 397 应无副本(CopyIDs 空), got %+v", cs[1])
	}
}

func TestPlanCreateGameUseExisting(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName:       "洛阳2服",
		WorldName:        "Y2",
		Slot:             GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp:      "game-rds",
		BattleEnabled:    true,
		ExistingBattleID: 12801,
		ExistingCenterID: 297,
	})
	if err != nil {
		t.Fatalf("PlanCreateGameServer: %v", err)
	}
	if len(plan.Rows) != 1 || plan.Rows[0].Kind != "game" {
		t.Fatalf("应只 1 个游戏服行, got %+v", plan.Rows)
	}
	g := plan.Rows[0].Fields
	if g["WorldType"] != "0" || g["BattleWorldID"] != "12801" || g["GlobalCenterWorldID"] != "297" {
		t.Errorf("game 关联 = %+v", g)
	}
	if g["SelfPublicIp"] != "10.0.0.1" || g["PortForClient"] != "3441" || g["MySqlIp"] != "game-rds" {
		t.Errorf("game 落点 = %+v", g)
	}
	// 手填名:ServerShowName/Desc=服务器名,WorldName=短码,WoRegion 留空(不分组)
	if g["ServerShowName"] != "洛阳2服" || g["Desc"] != "洛阳2服" || g["WorldName"] != "Y2" || g["WoRegion"] != "" {
		t.Errorf("命名字段 = %+v", g)
	}
	if plan.Rows[0].ID != 10002 {
		t.Errorf("Id = %d, want 10002", plan.Rows[0].ID)
	}
}

func TestPlanCreateGameNewBattleAndCenter(t *testing.T) {
	plan, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName:       "洛阳3服",
		WorldName:        "Y3",
		Slot:             GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp:      "game-rds",
		BattleEnabled:    true,
		NewBattleMachine: MachineFrom("10.1.0.2", "9.9.9.2"),
		BattleMySqlIp:    "battle-rds",
		NewCenterMachine: MachineFrom("192.168.11.161", "8.8.8.2"),
		CenterMySqlIp:    "center-rds",
	})
	if err != nil {
		t.Fatalf("PlanCreateGameServer: %v", err)
	}
	// game + battle + copy + center + center-copy 共 5 行;两个 WT3 副本都 Kind=copy
	var copies []PlannedRow
	kinds := map[string]PlannedRow{}
	for _, r := range plan.Rows {
		if r.Kind == "copy" {
			copies = append(copies, r)
		}
		kinds[r.Kind] = r
	}
	b, ok := kinds["battle"]
	if !ok || b.ID != 12802 || b.Fields["MySqlIp"] != "battle-rds" || b.Fields["SelfPublicIp"] != "10.1.0.2" {
		t.Errorf("battle = %+v", b)
	}
	ct, ok := kinds["center"]
	// 新规则:中心服 = WT4 max+1 = 297+1 = 298(旧共池规则曾是 12803)
	if !ok || ct.ID != 298 || ct.Fields["WorldName"] != "worldcenter298" ||
		ct.Fields["PortForClient"] != "3541" || ct.Fields["MySqlIp"] != "center-rds" ||
		ct.Fields["GlobalCenterWorldID"] != "298" || ct.Fields["BattleWorldID"] != "-1" {
		t.Errorf("center = %+v", ct)
	}
	if len(copies) != 2 {
		t.Fatalf("应有 2 个 WT3 副本(战斗副本 + 中心副本), got %d: %+v", len(copies), copies)
	}
	var battleCopy, centerCopy *PlannedRow
	for i := range copies {
		switch copies[i].ID {
		case 12002:
			battleCopy = &copies[i]
		case 12003:
			centerCopy = &copies[i]
		}
	}
	if battleCopy == nil || battleCopy.Fields["BattleWorldID"] != "12802" {
		t.Errorf("战斗副本 = %+v", battleCopy)
	}
	if centerCopy == nil || centerCopy.Fields["WorldName"] != "WorldCopyCenter12003" ||
		centerCopy.Fields["GlobalCenterWorldID"] != "298" || centerCopy.Fields["BattleWorldID"] != "-1" ||
		centerCopy.Fields["PortForClient"] != "3941" || centerCopy.Fields["MySqlIp"] != "center-rds" ||
		centerCopy.Fields["SelfPublicIp"] != "192.168.11.161" {
		t.Errorf("中心副本 = %+v", centerCopy)
	}
	g := kinds["game"].Fields
	if g["BattleWorldID"] != "12802" || g["GlobalCenterWorldID"] != "298" {
		t.Errorf("game 关联新建 = %+v", g)
	}
}

func TestPlanCreateGameNewCenterNeedsMySqlIp(t *testing.T) {
	_, err := PlanCreateGameServer(createFixture(), CreateGameParams{
		ServerName:       "洛阳4服",
		WorldName:        "Y4",
		Slot:             GameSlot{SelfPublicIp: "10.0.0.1", RealSelfPublicIp: "1.1.1.1", PortBase: 3400},
		GameMySqlIp:      "game-rds",
		BattleEnabled:    true,
		ExistingBattleID: 12801,
		NewCenterMachine: MachineFrom("192.168.11.161", "8.8.8.2"),
		// 故意不填 CenterMySqlIp
	})
	if err == nil {
		t.Fatal("新建中心服未填 CenterMySqlIp 应报错")
	}
}
