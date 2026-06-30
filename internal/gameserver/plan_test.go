package gameserver

import (
	"reflect"
	"strconv"
	"testing"
)

func TestApplyPortBand(t *testing.T) {
	row := map[string]string{}
	applyPortBand(row, 3400)
	want := map[string]string{
		"PortForClient":               "3441",
		"RealPortForClient":           "3441",
		"PortForGMServer":             "3442",
		"DBRedisPort":                 "3442",
		"DBPort":                      "3443",
		"HttpPort":                    "3444",
		"HttpServerCommonPort":        "3445",
		"PortForBigWorld":             "3450",
		"PortForBattleWorld":          "3451",
		"PortForBattleCopySceneWorld": "3452",
		"PortForGlobalCenter":         "3453",
	}
	if !reflect.DeepEqual(row, want) {
		t.Errorf("applyPortBand(3400) = %v, want %v", row, want)
	}
}

func TestAtoiFieldAndPortBaseOf(t *testing.T) {
	if got := atoiField(map[string]string{"Id": "10500"}, "Id"); got != 10500 {
		t.Errorf("atoiField Id = %d, want 10500", got)
	}
	if got := atoiField(map[string]string{"Id": "x"}, "Id"); got != 0 {
		t.Errorf("atoiField bad = %d, want 0", got)
	}
	// PortForClient=3541 -> base 3500
	if got := portBaseOf(map[string]string{"PortForClient": "3541"}); got != 3500 {
		t.Errorf("portBaseOf = %d, want 3500", got)
	}
}

func TestRegionHelpers(t *testing.T) {
	// 大区按 ServerShowName(中文展示名)归组;WorldName 是短码(字母前缀+序号)。
	rows := []map[string]string{
		{"Id": "10001", "WorldType": "0", "WorldName": "Y1", "ServerShowName": "洛阳1服"},
		{"Id": "10002", "WorldType": "0", "WorldName": "Y3", "ServerShowName": "洛阳3服"},
		{"Id": "10003", "WorldType": "0", "WorldName": "C2", "ServerShowName": "长安2服"},
		{"Id": "12801", "WorldType": "2", "WorldName": "WorldWar12801", "ServerShowName": ""}, // 战斗服无ShowName,忽略
	}
	if got := regionSeq("洛阳73服"); got != 73 {
		t.Errorf("regionSeq = %d, want 73", got)
	}
	if got := regionSeq("无数字"); got != 0 {
		t.Errorf("regionSeq no-num = %d, want 0", got)
	}
	if got := regionSeq("洛阳5服(正式服)"); got != 5 { // 带括号标签也应取到序号
		t.Errorf("regionSeq with paren = %d, want 5", got)
	}
	if got := nextRegionSeq(rows, "洛阳"); got != 3 {
		t.Errorf("nextRegionSeq 洛阳 = %d, want 3", got)
	}
	if got := nextRegionSeq(rows, "新区"); got != 0 {
		t.Errorf("nextRegionSeq 新区 = %d, want 0", got)
	}
	got := existingRegions(rows)
	want := []string{"洛阳", "长安"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("existingRegions = %v, want %v", got, want)
	}
	// 字母前缀:已有大区从该区(取最大Id的)游戏服 WorldName 去尾号推导
	if got := regionLetterPrefix(rows, "洛阳"); got != "Y" {
		t.Errorf("regionLetterPrefix 洛阳 = %q, want Y", got)
	}
	if got := regionLetterPrefix(rows, "长安"); got != "C" {
		t.Errorf("regionLetterPrefix 长安 = %q, want C", got)
	}
}

func TestPickGameSlotsCompact(t *testing.T) {
	rows := []map[string]string{
		// 机器 A(10.0.0.1):已用 2 档(3300/3400),剩 1 档(3500)
		{"Id": "10001", "WorldType": "0", "SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1", "PortForClient": "3341"},
		{"Id": "10002", "WorldType": "0", "SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1", "PortForClient": "3441"},
		// 机器 B(10.0.0.2):空(0 档在用),剩 3 档
		{"Id": "10003", "WorldType": "-1", "SelfPublicIp": "10.0.0.2", "RealSelfPublicIp": "2.2.2.2", "PortForClient": "3341"}, // 废弃,不占
	}
	got, err := pickGameSlots(rows, 2)
	if err != nil {
		t.Fatalf("pickGameSlots: %v", err)
	}
	// 紧凑:先填空闲档最少的 A(剩1档,base 3500),再填 B 的最小档(3300)
	want := []slot{
		{selfIp: "10.0.0.1", realIp: "1.1.1.1", base: 3500},
		{selfIp: "10.0.0.2", realIp: "2.2.2.2", base: 3300},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pickGameSlots = %+v, want %+v", got, want)
	}
}

// 低 Id 的在用游戏服(WT0 但 Id<10000)也占档:同机 235 占 3300、10000 占 3500,
// 唯一空槽是 3400;请求 1 个应落 3400,请求 2 个应空槽不足报错。
func TestPickGameSlotsLowIdOccupies(t *testing.T) {
	rows := []map[string]string{
		{"Id": "235", "WorldType": "0", "SelfPublicIp": "192.168.11.166", "RealSelfPublicIp": "9.9.9.9", "PortForClient": "3341"},
		{"Id": "10000", "WorldType": "0", "SelfPublicIp": "192.168.11.166", "RealSelfPublicIp": "9.9.9.9", "PortForClient": "3541"},
	}
	got, err := pickGameSlots(rows, 1)
	if err != nil {
		t.Fatalf("pickGameSlots: %v", err)
	}
	want := []slot{{selfIp: "192.168.11.166", realIp: "9.9.9.9", base: 3400}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("应落唯一空闲档 3400, got %+v", got)
	}
	if _, err := pickGameSlots(rows, 2); err == nil {
		t.Fatal("仅 1 个空槽,请求 2 个应报错")
	}
}

func TestPickGameSlotsInsufficient(t *testing.T) {
	rows := []map[string]string{
		{"Id": "10001", "WorldType": "0", "SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1", "PortForClient": "3341"},
		{"Id": "10002", "WorldType": "0", "SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1", "PortForClient": "3441"},
		{"Id": "10003", "WorldType": "0", "SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1", "PortForClient": "3541"},
	}
	if _, err := pickGameSlots(rows, 1); err == nil {
		t.Fatal("expected error when no free slot")
	}
}

func TestFreeBattleMachines(t *testing.T) {
	rows := []map[string]string{
		// 机器 X:有在用战斗对(WT2/WT3)-> 占用
		{"Id": "12801", "WorldType": "2", "SelfPublicIp": "10.1.0.1", "RealSelfPublicIp": "1.1.1.1"},
		{"Id": "12001", "WorldType": "3", "SelfPublicIp": "10.1.0.1", "RealSelfPublicIp": "1.1.1.1"},
		// 机器 Y:曾是战斗机(Id>12000),现在全废弃 -> 空闲
		{"Id": "12802", "WorldType": "-1", "SelfPublicIp": "10.1.0.2", "RealSelfPublicIp": "2.2.2.2"},
		{"Id": "12002", "WorldType": "-1", "SelfPublicIp": "10.1.0.2", "RealSelfPublicIp": "2.2.2.2"},
	}
	got := freeBattleMachines(rows)
	want := []machine{{selfIp: "10.1.0.2", realIp: "2.2.2.2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("freeBattleMachines = %+v, want %+v", got, want)
	}
}

func TestNextIDs(t *testing.T) {
	rows := []map[string]string{
		{"Id": "10001", "WorldType": "0"},
		{"Id": "10005", "WorldType": "0"},
		{"Id": "10003", "WorldType": "-1"}, // 废弃不算正式最大
		{"Id": "12801", "WorldType": "2"},
		{"Id": "12001", "WorldType": "3"},
	}
	gids, err := nextGameIDs(rows, 2)
	if err != nil || !reflect.DeepEqual(gids, []int{10006, 10007}) {
		t.Fatalf("nextGameIDs = %v, err=%v, want [10006 10007]", gids, err)
	}
}

// 发号永远基于「所有 WT0 的最大值」+1,不再因区间过滤回退到 10000:
// 仅有低 Id 游戏服 235 时,下一个应是 236(而非旧规则的 10000+)。
func TestNextGameIDsFromMaxWT0(t *testing.T) {
	rows := []map[string]string{
		{"Id": "235", "WorldType": "0"},
		{"Id": "10000", "WorldType": "-1"}, // 废弃服,不是 WT0 基准
	}
	gids, err := nextGameIDs(rows, 1)
	if err != nil || !reflect.DeepEqual(gids, []int{236}) {
		t.Fatalf("nextGameIDs = %v, err=%v, want [236]", gids, err)
	}
}

// 下一个号被废弃服占用时自动跳过(+1),不再报错。
func TestNextGameIDsSkipsOccupied(t *testing.T) {
	rows := []map[string]string{
		{"Id": "10005", "WorldType": "0"},
		{"Id": "10006", "WorldType": "-1"}, // 占了 10006 -> 跳到 10007
	}
	gids, err := nextGameIDs(rows, 1)
	if err != nil || !reflect.DeepEqual(gids, []int{10007}) {
		t.Fatalf("nextGameIDs = %v, err=%v, want [10007]", gids, err)
	}
}

// 配置库无任何 WT0 游戏服 → 无发号基准,报错。
func TestNextGameIDsNoBase(t *testing.T) {
	rows := []map[string]string{{"Id": "12801", "WorldType": "2"}}
	if _, err := nextGameIDs(rows, 1); err == nil {
		t.Fatal("无 WT0 基准应报错")
	}
}

func TestLatestTemplate(t *testing.T) {
	rows := []map[string]string{
		{"Id": "10001", "WorldType": "0", "WorldName": "old"},
		{"Id": "10005", "WorldType": "0", "WorldName": "newest"},
	}
	tpl := latestTemplate(rows, 0)
	if tpl == nil || tpl["WorldName"] != "newest" {
		t.Fatalf("latestTemplate = %v, want newest", tpl)
	}
	// 改返回值不应影响原行(深拷贝)
	tpl["WorldName"] = "mutated"
	if rows[1]["WorldName"] != "newest" {
		t.Fatal("latestTemplate must deep-copy")
	}
	if latestTemplate(rows, 2) != nil {
		t.Fatal("no WT2 -> nil")
	}
}

// 构造一份最小但够用的现有配置:游戏服模板 + 第二台空游戏机 + 战斗/副本模板 + 空闲战斗机。
// 注意:Count=4 需要 ≥4 个游戏空槽,单机最多 3 档,故需两台游戏机;
// 空闲战斗机的 Id 要避开将被分配的 12802/12002,否则唯一性护栏会判冲突。
func planFixture() []map[string]string {
	return []map[string]string{
		// 游戏服模板(最新 WT0),机器 GA 已用 1 档(3300),剩 2 档;WorldName 短码=Y1,大区=洛阳
		{"Id": "10001", "WorldType": "0", "WorldName": "Y1", "ServerShowName": "洛阳1服", "Desc": "洛阳1服",
			"SelfPublicIp": "10.0.0.1", "RealSelfPublicIp": "1.1.1.1", "PortForClient": "3341", "DataBaseUser": "root",
			"DataBasePsw": "pw", "DBIP": "127.0.0.1", "BattleWorldID": "12801", "RealWorldID": "10001",
			"RealSelfPublicUrl": "wss://ws-tw.tlh5.qinxiand.com/s10001"},
		// 第二台游戏机 GB(只有一台废弃服 -> 进游戏池但 0 在用,剩 3 档),凑够 Count=4 的空槽
		{"Id": "11000", "WorldType": "-1", "SelfPublicIp": "10.0.0.3", "RealSelfPublicIp": "1.1.1.3",
			"PortForClient": "3341"},
		// 战斗服模板(占用机器 BX)
		{"Id": "12801", "WorldType": "2", "WorldName": "WorldWar12801", "SelfPublicIp": "10.1.0.1",
			"RealSelfPublicIp": "9.9.9.1", "PortForClient": "3841", "DataBaseUser": "root", "RealWorldID": "12801",
			"RealSelfPublicUrl": "wss://ws-tw.tlh5.qinxiand.com/s12801"},
		// 副本模板(机器 BX)
		{"Id": "12001", "WorldType": "3", "WorldName": "WorldCopyWar12001", "SelfPublicIp": "10.1.0.1",
			"RealSelfPublicIp": "9.9.9.1", "PortForClient": "3941", "DataBaseUser": "root", "RealWorldID": "12001",
			"RealSelfPublicUrl": "wss://ws-tw.tlh5.qinxiand.com/s12001"},
		// 空闲战斗机 BY(Id>12000 但全废弃;Id 避开将分配的 12802/12002)
		{"Id": "12810", "WorldType": "-1", "SelfPublicIp": "10.1.0.2", "RealSelfPublicIp": "9.9.9.2"},
		{"Id": "12010", "WorldType": "-1", "SelfPublicIp": "10.1.0.2", "RealSelfPublicIp": "9.9.9.2"},
	}
}

// withCenterTpl 在 fixture 上追加一台 WT4 中心服(兼作模板),Id 13000。
func withCenterTpl() []map[string]string {
	return append(planFixture(), map[string]string{
		"Id": "13000", "WorldType": "4", "WorldName": "worldcenter13000",
		"SelfPublicIp": "10.2.0.1", "RealSelfPublicIp": "8.8.8.1", "PortForClient": "3541",
		"DataBaseUser": "root", "RealWorldID": "13000",
	})
}

func TestPlanNewServersExistingCenter(t *testing.T) {
	plan, err := PlanNewServers(withCenterTpl(), NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "game-rds", BattleMySqlIp: "battle-rds",
		ExistingCenterID: 13000,
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	var games, centers int
	for _, r := range plan.Rows {
		switch r.Kind {
		case "game":
			games++
			if r.Fields["GlobalCenterWorldID"] != "13000" {
				t.Errorf("游戏服应挂中心服 13000, got %q", r.Fields["GlobalCenterWorldID"])
			}
		case "center":
			centers++
		}
	}
	if games != 4 || centers != 0 {
		t.Errorf("挂现有中心服不应新建中心: games=%d centers=%d", games, centers)
	}
}

func TestPlanNewServersNewCenter(t *testing.T) {
	plan, err := PlanNewServers(withCenterTpl(), NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "game-rds", BattleMySqlIp: "battle-rds",
		NewCenterMachine: MachineFrom("10.3.0.1", "8.8.8.9"), CenterMySqlIp: "center-rds",
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	var games, centers, copies int
	var centerID string
	for _, r := range plan.Rows {
		switch r.Kind {
		case "game":
			games++
		case "center":
			centers++
			centerID = r.Fields["Id"]
			if r.Fields["WorldType"] != "4" || r.Fields["MySqlIp"] != "center-rds" ||
				r.Fields["SelfPublicIp"] != "10.3.0.1" {
				t.Errorf("center fields = %+v", r.Fields)
			}
		case "copy":
			copies++
		}
	}
	// 4 游戏 + 1 战斗 + 1 战斗副本 + 1 中心 + 1 中心副本
	if games != 4 || centers != 1 || copies != 2 {
		t.Fatalf("kinds: game=%d center=%d copy=%d, want 4/1/2", games, centers, copies)
	}
	for _, r := range plan.Rows {
		if r.Kind == "game" && r.Fields["GlobalCenterWorldID"] != centerID {
			t.Errorf("游戏服 GlobalCenterWorldID=%q, want %q", r.Fields["GlobalCenterWorldID"], centerID)
		}
	}
}

func TestPlanNewServersHappy(t *testing.T) {
	plan, err := PlanNewServers(planFixture(), NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "game-rds", BattleMySqlIp: "battle-rds",
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	// 4 游戏 + 1 战斗 + 1 副本 = 6 行
	if len(plan.Rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(plan.Rows))
	}
	var games, battles, copies []PlannedRow
	for _, r := range plan.Rows {
		switch r.Kind {
		case "game":
			games = append(games, r)
		case "battle":
			battles = append(battles, r)
		case "copy":
			copies = append(copies, r)
		}
	}
	if len(games) != 4 || len(battles) != 1 || len(copies) != 1 {
		t.Fatalf("kinds = g%d b%d c%d", len(games), len(battles), len(copies))
	}
	b := battles[0]
	if b.ID != 12802 || b.Fields["WorldName"] != "WorldWar12802" || b.Fields["BattleWorldID"] != "12802" {
		t.Errorf("battle = %+v", b.Fields)
	}
	if b.Fields["SelfPublicIp"] != "10.1.0.2" || b.Fields["PortForClient"] != "3841" || b.Fields["MySqlIp"] != "battle-rds" {
		t.Errorf("battle fields = %+v", b.Fields)
	}
	cp := copies[0]
	if cp.ID != 12002 || cp.Fields["WorldName"] != "WorldCopyWar12002" || cp.Fields["BattleWorldID"] != "12802" ||
		cp.Fields["SelfPublicIp"] != "10.1.0.2" || cp.Fields["PortForClient"] != "3941" {
		t.Errorf("copy fields = %+v", cp.Fields)
	}
	// 游戏服:Id 10002..10005、ServerShowName=洛阳2..5服、WorldName 短码=Y2..Y5(沿用大区字母 Y)、
	// Desc=ServerShowName、BattleWorldID=12802、MySqlIp=game-rds、DataBaseName=ptdb_<Id>、沿用模板 DataBaseUser
	g0 := games[0]
	if g0.ID != 10002 || g0.Fields["ServerShowName"] != "洛阳2服" || g0.Fields["WorldName"] != "Y2" ||
		g0.Fields["Desc"] != "洛阳2服" || g0.Fields["BattleWorldID"] != "12802" ||
		g0.Fields["MySqlIp"] != "game-rds" || g0.Fields["DataBaseName"] != "ptdb_10002" ||
		g0.Fields["DataBaseUser"] != "root" || g0.Fields["WoRegion"] != "洛阳" {
		t.Errorf("game0 = %+v", g0.Fields)
	}
	if games[3].Fields["ServerShowName"] != "洛阳5服" || games[3].Fields["WorldName"] != "Y5" {
		t.Errorf("game3 = ShowName %s / WorldName %s, want 洛阳5服 / Y5",
			games[3].Fields["ServerShowName"], games[3].Fields["WorldName"])
	}
}

// 添加外网地址(AddPublicUrl=true):RealSelfPublicUrl 按类型生成 wss 域名 + /s<id>:游戏服 gs-tw,战斗/副本 ws-tw。
func TestPlanNewServersPublicUrlByType(t *testing.T) {
	plan, err := PlanNewServers(planFixture(), NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "game-rds", BattleMySqlIp: "battle-rds",
		AddPublicUrl: true,
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	want := map[int]string{
		10002: "wss://gs-tw.tlh5.qinxiand.com/s10002", // 游戏服 → gs-tw
		12802: "wss://ws-tw.tlh5.qinxiand.com/s12802", // 战斗服 → ws-tw
		12002: "wss://ws-tw.tlh5.qinxiand.com/s12002", // 副本服 → ws-tw
	}
	for _, r := range plan.Rows {
		if exp, ok := want[r.ID]; ok {
			if got := r.Fields["RealSelfPublicUrl"]; got != exp {
				t.Errorf("Id %d RealSelfPublicUrl = %q, want %q", r.ID, got, exp)
			}
		}
	}
}

// RealWorldID 对有效服恒等于自身 Id(模板里是模板服的旧 Id,必须被覆盖)。
func TestPlanNewServersSetsRealWorldID(t *testing.T) {
	plan, err := PlanNewServers(planFixture(), NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "game-rds", BattleMySqlIp: "battle-rds",
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	for _, r := range plan.Rows {
		if got := r.Fields["RealWorldID"]; got != strconv.Itoa(r.ID) {
			t.Errorf("Id %d RealWorldID = %q, want %d", r.ID, got, r.ID)
		}
	}
}

// 不添加外网地址(AddPublicUrl=false):RealSelfPublicUrl 直接填外网IP(RealSelfPublicIp)。
func TestPlanNewServersPublicUrlAsIP(t *testing.T) {
	plan, err := PlanNewServers(planFixture(), NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "game-rds", BattleMySqlIp: "battle-rds",
		// AddPublicUrl 默认 false
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	for _, r := range plan.Rows {
		ip := r.Fields["RealSelfPublicIp"]
		if got := r.Fields["RealSelfPublicUrl"]; got != ip {
			t.Errorf("Id %d 不加外网地址应填外网IP %q, got %q", r.ID, ip, got)
		}
	}
}

func TestPlanNewServersNewRegion(t *testing.T) {
	// 新开大区「凤凰」,运维手填字母前缀 F;序号从 1 起
	plan, err := PlanNewServers(planFixture(), NewServerParams{
		RegionName: "凤凰", LetterPrefix: "F", Count: 4, GameMySqlIp: "g", BattleMySqlIp: "b",
	})
	if err != nil {
		t.Fatalf("PlanNewServers: %v", err)
	}
	var games []PlannedRow
	for _, r := range plan.Rows {
		if r.Kind == "game" {
			games = append(games, r)
		}
	}
	if games[0].Fields["ServerShowName"] != "凤凰1服" || games[0].Fields["WorldName"] != "F1" ||
		games[3].Fields["ServerShowName"] != "凤凰4服" || games[3].Fields["WorldName"] != "F4" {
		t.Errorf("new region naming wrong: g0=%q/%q g3=%q/%q",
			games[0].Fields["ServerShowName"], games[0].Fields["WorldName"],
			games[3].Fields["ServerShowName"], games[3].Fields["WorldName"])
	}
}

func TestPlanNewServersNewRegionNeedsLetter(t *testing.T) {
	// 新大区但没填字母前缀 -> 报错
	if _, err := PlanNewServers(planFixture(), NewServerParams{
		RegionName: "凤凰", LetterPrefix: "", Count: 4, GameMySqlIp: "g", BattleMySqlIp: "b",
	}); err == nil {
		t.Fatal("expected error: new region needs letter prefix")
	}
}

func TestPlanNewServersValidation(t *testing.T) {
	rows := planFixture()
	bad := []NewServerParams{
		{RegionName: "洛阳", Count: 3, GameMySqlIp: "a", BattleMySqlIp: "b"}, // 非 4 倍数
		{RegionName: "洛阳", Count: 0, GameMySqlIp: "a", BattleMySqlIp: "b"}, // 0
		{RegionName: "", Count: 4, GameMySqlIp: "a", BattleMySqlIp: "b"},   // 缺大区
		{RegionName: "洛阳", Count: 4, GameMySqlIp: "", BattleMySqlIp: "b"},  // 缺游戏 MySqlIp
		{RegionName: "洛阳", Count: 4, GameMySqlIp: "a", BattleMySqlIp: ""},  // 缺战斗 MySqlIp
	}
	for i, p := range bad {
		if _, err := PlanNewServers(rows, p); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestRowRegionPrefersWoRegion(t *testing.T) {
	if got := rowRegion(map[string]string{"WoRegion": "凤凰", "ServerShowName": "洛阳7服"}); got != "凤凰" {
		t.Errorf("rowRegion = %q, want 凤凰", got)
	}
	if got := rowRegion(map[string]string{"ServerShowName": "洛阳7服"}); got != "洛阳" {
		t.Errorf("rowRegion 回退 = %q, want 洛阳", got)
	}
}

func TestPlanNewServersNoBattleMachine(t *testing.T) {
	// 去掉空闲战斗机 BY(10.1.0.2),保留游戏机 + 被占用的战斗机 BX -> 无空闲战斗机
	var rows []map[string]string
	for _, r := range planFixture() {
		if r["SelfPublicIp"] == "10.1.0.2" {
			continue
		}
		rows = append(rows, r)
	}
	if _, err := PlanNewServers(rows, NewServerParams{
		RegionName: "洛阳", Count: 4, GameMySqlIp: "a", BattleMySqlIp: "b",
	}); err == nil {
		t.Fatal("expected error: no free battle machine")
	}
}

func TestNextIDPools(t *testing.T) {
	rows := []map[string]string{
		{"Id": "12801", "WorldType": "2"},
		{"Id": "12001", "WorldType": "3"},
		{"Id": "297", "WorldType": "4"},
	}
	used := map[int]bool{}
	// 战斗+中心合并池:max(12801,297)+1
	id, err := nextID(rows, used, 2, 4)
	if err != nil || id != 12802 {
		t.Fatalf("nextID(2,4) = %d, err=%v, want 12802", id, err)
	}
	used[id] = true
	// 再取一次跳过已占用 -> 12803
	id2, err := nextID(rows, used, 2, 4)
	if err != nil || id2 != 12803 {
		t.Fatalf("nextID(2,4) again = %d, err=%v, want 12803", id2, err)
	}
	// 副本池
	cid, err := nextID(rows, used, 3)
	if err != nil || cid != 12002 {
		t.Fatalf("nextID(3) = %d, err=%v, want 12002", cid, err)
	}
	// 无匹配类型 -> 报错
	if _, err := nextID(rows, used, 1); err == nil {
		t.Fatal("nextID(1) 应报错(无 WT1 发号基准)")
	}
}
