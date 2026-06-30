package gameserver

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// 端口档内各端口相对 base 的偏移。
var portOffsets = map[string]int{
	"PortForClient":               41,
	"RealPortForClient":           41,
	"PortForGMServer":             42,
	"DBRedisPort":                 42,
	"DBPort":                      43,
	"HttpPort":                    44,
	"HttpServerCommonPort":        45,
	"PortForBigWorld":             50,
	"PortForBattleWorld":          51,
	"PortForBattleCopySceneWorld": 52,
	"PortForGlobalCenter":         53,
}

// applyPortBand 按 base 把整档端口列写入 row(覆盖)。
func applyPortBand(row map[string]string, base int) {
	for col, off := range portOffsets {
		row[col] = strconv.Itoa(base + off)
	}
}

// publicUrlRoot 外网地址根域(环境相关;改域名在此调整,或后续抽到配置)。
const publicUrlRoot = "tlh5.qinxiand.com"

// setPublicUrl 按「是否添加外网地址」设置 RealSelfPublicUrl:
//   - addUrl=true  → wss://<gs-tw|ws-tw>.<root>/s<Id>(游戏服 WT0 用 gs-tw,战斗/副本/中心用 ws-tw);
//   - addUrl=false → 直接填外网IP(RealSelfPublicIp)。
//
// 读 row 自身的 WorldType / Id / RealSelfPublicIp,调用前这三项须已写入。
func setPublicUrl(row map[string]string, addUrl bool) {
	if !addUrl {
		row["RealSelfPublicUrl"] = row["RealSelfPublicIp"]
		return
	}
	host := "ws-tw"
	if atoiField(row, "WorldType") == 0 {
		host = "gs-tw"
	}
	row["RealSelfPublicUrl"] = fmt.Sprintf("wss://%s.%s/s%d", host, publicUrlRoot, atoiField(row, "Id"))
}

// atoiField 取列的整数值;缺失或非数字返回 0。
func atoiField(row map[string]string, col string) int {
	n, _ := strconv.Atoi(row[col])
	return n
}

// portBaseOf 由 PortForClient 反推该服的端口档 base(3341 -> 3300)。
func portBaseOf(row map[string]string) int {
	return (atoiField(row, "PortForClient") / 100) * 100
}

// regionSeqRe 匹配 WorldName 结尾的序号(可带「服」)。
var regionSeqRe = regexp.MustCompile(`([0-9]+)服?$`)

// regionSeq 取 WorldName 结尾序号;无则 0。先剥掉 (...) 标签,与 GroupName 对齐
// (例:"洛阳5服(正式服)" -> 5,而非取不到)。
func regionSeq(worldName string) int {
	s := parenRe.ReplaceAllString(worldName, "")
	m := regionSeqRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// 大区与命名一律基于 ServerShowName(中文展示名,如 "洛阳73服");WorldName 是短码(字母前缀+序号)。

// rowRegion 取一行的大区:优先工单系统自维护的 WoRegion;空则回退 GroupName(ServerShowName)。
func rowRegion(r map[string]string) string {
	if v := r["WoRegion"]; v != "" {
		return v
	}
	return GroupName(r["ServerShowName"])
}

// nextRegionSeq 返回某大区现有正式游戏服(WT0)的最大序号;无则 0。
func nextRegionSeq(rows []map[string]string, region string) int {
	maxSeq := 0
	for _, r := range rows {
		if atoiField(r, "WorldType") != 0 {
			continue
		}
		if rowRegion(r) != region {
			continue
		}
		if s := regionSeq(r["ServerShowName"]); s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq
}

// existingRegions 返回现有正式游戏服(WT0)的大区(ServerShowName 前缀)去重升序列表(供下拉)。
func existingRegions(rows []map[string]string) []string {
	set := map[string]bool{}
	for _, r := range rows {
		if atoiField(r, "WorldType") != 0 {
			continue
		}
		set[rowRegion(r)] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// trailDigitsRe 匹配 WorldName 短码结尾的数字(用于取字母前缀:"Y43" -> "Y")。
var trailDigitsRe = regexp.MustCompile(`[0-9]+$`)

// regionLetterPrefix 返回某大区 WorldName 短码的字母前缀:取该区(最大 Id 的)正式游戏服
// 的 WorldName 去掉尾部数字。无该区则空串。
func regionLetterPrefix(rows []map[string]string, region string) string {
	best := ""
	bestID := -1
	for _, r := range rows {
		if atoiField(r, "WorldType") != 0 {
			continue
		}
		if rowRegion(r) != region {
			continue
		}
		if id := atoiField(r, "Id"); id > bestID {
			bestID = id
			best = r["WorldName"]
		}
	}
	return trailDigitsRe.ReplaceAllString(best, "")
}

// slot 一个游戏服落点:目标机器 + 端口档 base。
type slot struct {
	selfIp string
	realIp string
	base   int
}

var gameBands = []int{3300, 3400, 3500}

// pickGameSlots 紧凑装箱选出 count 个游戏服落点。空槽不足返回错误。
func pickGameSlots(rows []map[string]string, count int) ([]slot, error) {
	type mach struct {
		realIp string
		used   map[int]bool
	}
	machines := map[string]*mach{}
	for _, r := range rows {
		id := atoiField(r, "Id")
		isGame := atoiField(r, "WorldType") == 0
		// 机器入游戏池的判据:落在发号区间(含废弃服,机器仍属游戏机队)或本身就是在用游戏服。
		if (id < gameIDMin || id > gameIDMax) && !isGame {
			continue
		}
		sip := r["SelfPublicIp"]
		if sip == "" {
			continue
		}
		m := machines[sip]
		if m == nil {
			m = &mach{realIp: r["RealSelfPublicIp"], used: map[int]bool{}}
			machines[sip] = m
		}
		if m.realIp == "" {
			m.realIp = r["RealSelfPublicIp"]
		}
		// 占档只看在用游戏服(WT0),与 Id 无关:低 Id 老服(如 235)同样占其端口档。
		if isGame {
			m.used[portBaseOf(r)] = true
		}
	}

	type cand struct {
		selfIp, realIp string
		free           []int
	}
	var cands []cand
	for sip, m := range machines {
		var free []int
		for _, b := range gameBands {
			if !m.used[b] {
				free = append(free, b)
			}
		}
		if len(free) > 0 {
			cands = append(cands, cand{selfIp: sip, realIp: m.realIp, free: free})
		}
	}
	// 紧凑:空闲档少的优先,SelfIp 升序兜底(确定性)
	sort.Slice(cands, func(i, j int) bool {
		if len(cands[i].free) != len(cands[j].free) {
			return len(cands[i].free) < len(cands[j].free)
		}
		return cands[i].selfIp < cands[j].selfIp
	})

	var slots []slot
	for _, c := range cands {
		for _, b := range c.free { // free 已按 gameBands 顺序(3300<3400<3500)
			slots = append(slots, slot{selfIp: c.selfIp, realIp: c.realIp, base: b})
		}
	}
	if len(slots) < count {
		return nil, fmt.Errorf("游戏服空槽不足:需要 %d,可用 %d", count, len(slots))
	}
	return slots[:count], nil
}

// machine 一台机器的两个 IP。
type machine struct {
	selfIp string
	realIp string
}

const (
	gameIDMin   = 10000
	gameIDMax   = 12000
	battleIDMin = 12000
	battleBase  = 3800
	copyBase    = 3900
)

// idSet 配置中所有已占用的 Id 集合。
func idSet(rows []map[string]string) map[int]bool {
	set := map[int]bool{}
	for _, r := range rows {
		set[atoiField(r, "Id")] = true
	}
	return set
}

// freeBattleMachines 战斗机器池(Id>12000 行的 SelfIp)中当前无在用战斗对(WT2/WT3)的机器,按 SelfIp 升序。
func freeBattleMachines(rows []map[string]string) []machine {
	realIp := map[string]string{}
	occupied := map[string]bool{}
	var order []string
	for _, r := range rows {
		if atoiField(r, "Id") <= battleIDMin {
			continue
		}
		sip := r["SelfPublicIp"]
		if sip == "" {
			continue
		}
		if _, ok := realIp[sip]; !ok {
			order = append(order, sip)
		}
		if realIp[sip] == "" {
			realIp[sip] = r["RealSelfPublicIp"]
		}
		if wt := atoiField(r, "WorldType"); wt == 2 || wt == 3 {
			occupied[sip] = true
		}
	}
	var out []machine
	for _, sip := range order {
		if !occupied[sip] {
			out = append(out, machine{selfIp: sip, realIp: realIp[sip]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].selfIp < out[j].selfIp })
	return out
}

// nextID 返回 WorldType ∈ wts 的行里最大 Id + 1;跳过 used 已占用的 Id。
// 该集合无任何匹配行则报错(无发号基准)。
func nextID(rows []map[string]string, used map[int]bool, wts ...int) (int, error) {
	match := func(wt int) bool {
		for _, w := range wts {
			if w == wt {
				return true
			}
		}
		return false
	}
	maxID := -1
	for _, r := range rows {
		if match(atoiField(r, "WorldType")) {
			if id := atoiField(r, "Id"); id > maxID {
				maxID = id
			}
		}
	}
	if maxID < 0 {
		return 0, fmt.Errorf("配置库无 WorldType=%v 的服,无法发号", wts)
	}
	id := maxID + 1
	for used[id] {
		id++
	}
	return id, nil
}

// pickID 发号:手填值(manual>0)优先,校验未被占用后直接用;否则按 wts 号池 max+1
// (nextID 在号池无基准时报错)。
func pickID(manual int, rows []map[string]string, used map[int]bool, wts ...int) (int, error) {
	if manual > 0 {
		if used[manual] {
			return 0, fmt.Errorf("手填起始号 %d 已被占用", manual)
		}
		return manual, nil
	}
	return nextID(rows, used, wts...)
}

// nextGameIDs 取 count 个游戏服 Id:从 max(所有 WT0 游戏服 Id)+1 起、跳过 used(含废弃服)。
// 与单服向导一致——永远在已有最大值上递增(不再限定 [10000,12000] 区间、不回退 10000、无上限);
// 配置库无任何 WT0 游戏服时报错(无发号基准)。
func nextGameIDs(rows []map[string]string, count int) ([]int, error) {
	used := idSet(rows)
	maxID := -1
	for _, r := range rows {
		if atoiField(r, "WorldType") == 0 {
			if id := atoiField(r, "Id"); id > maxID {
				maxID = id
			}
		}
	}
	if maxID < 0 {
		return nil, fmt.Errorf("配置库无游戏服(WorldType=0),无法发号")
	}
	out := make([]int, count)
	id := maxID
	for i := 0; i < count; i++ {
		id++
		for used[id] {
			id++
		}
		out[i] = id
		used[id] = true
	}
	return out, nil
}

// latestTemplate 返回指定 WorldType 中 Id 最大的行的深拷贝;无则 nil。
func latestTemplate(rows []map[string]string, wt int) map[string]string {
	var best map[string]string
	bestID := -1
	for _, r := range rows {
		if atoiField(r, "WorldType") != wt {
			continue
		}
		if id := atoiField(r, "Id"); id > bestID {
			bestID = id
			best = r
		}
	}
	if best == nil {
		return nil
	}
	clone := make(map[string]string, len(best))
	for k, v := range best {
		clone[k] = v
	}
	return clone
}

// NewServerParams 新增游戏服输入。
type NewServerParams struct {
	RegionName    string // 大区名(ServerShowName 前缀;已有大区:选中的;新开大区:输入的)
	LetterPrefix  string // WorldName 短码字母前缀;新开大区时由运维手填,已有大区忽略(从库内推导)
	Count         int    // 新增游戏服数量(>0 且 4 的倍数)
	GameMySqlIp   string // 游戏服 MySqlIp(手填)
	BattleMySqlIp string // 战斗/副本服 MySqlIp(手填)
	AddPublicUrl  bool   // 是否添加外网域名地址:true=wss://<gs-tw|ws-tw>.../s<id>;false=填外网IP

	// 中心服(可选,整批共用一个):ExistingCenterID>0 挂现有;否则 NewCenterMachine 非空则新建一个
	// (WT4 中心服 + WT3 中心副本),全部游戏服都挂它。两者都空 = 不配置中心服。
	ExistingCenterID int
	NewCenterMachine machine
	CenterMySqlIp    string
}

// isCenterID rows 中是否存在该 Id 的中心服(WT4)。
func isCenterID(rows []map[string]string, id int) bool {
	for _, r := range rows {
		if atoiField(r, "WorldType") == 4 && atoiField(r, "Id") == id {
			return true
		}
	}
	return false
}

// PlannedRow 一个待插入行 + 预览摘要。
type PlannedRow struct {
	Kind           string // "game" | "battle" | "copy"
	ID             int
	Fields         map[string]string // 完整待插入行(列名->值)
	WorldName      string
	ServerShowName string
	SelfPublicIp   string
	PortBase       int
	BattleWorldID  int
	DataBaseName   string
	MySqlIp        string
}

// NewServerPlan 计算结果(预览/写入共用)。
type NewServerPlan struct {
	Rows []PlannedRow
}

// PlanNewServers 纯函数:由当前配置行 + 参数算出待插入新行。任何容量/校验不过则整体报错。
func PlanNewServers(rows []map[string]string, p NewServerParams) (*NewServerPlan, error) {
	if p.Count <= 0 || p.Count%4 != 0 {
		return nil, fmt.Errorf("数量必须为正整数且为 4 的倍数")
	}
	if p.RegionName == "" {
		return nil, fmt.Errorf("大区名不能为空")
	}
	if p.GameMySqlIp == "" || p.BattleMySqlIp == "" {
		return nil, fmt.Errorf("游戏服 / 战斗服 MySqlIp 均需填写")
	}
	pairs := p.Count / 4

	gameTpl := latestTemplate(rows, 0)
	battleTpl := latestTemplate(rows, 2)
	copyTpl := latestTemplate(rows, 3)
	if gameTpl == nil || battleTpl == nil || copyTpl == nil {
		return nil, fmt.Errorf("缺少游戏服/战斗服/副本服模板(配置库需已有对应类型的服)")
	}

	// WorldName 字母前缀:已有大区从库内推导;新开大区用运维手填。
	letter := regionLetterPrefix(rows, p.RegionName)
	if letter == "" {
		letter = p.LetterPrefix
	}
	if letter == "" {
		return nil, fmt.Errorf("新开大区需填写 WorldName 字母前缀")
	}

	slots, err := pickGameSlots(rows, p.Count)
	if err != nil {
		return nil, err
	}
	battleMachines := freeBattleMachines(rows)
	if len(battleMachines) < pairs {
		return nil, fmt.Errorf("空闲战斗机器不足:需要 %d,可用 %d", pairs, len(battleMachines))
	}
	gameIDs, err := nextGameIDs(rows, p.Count)
	if err != nil {
		return nil, err
	}
	// 战斗服与中心服共用一个号池(中心服本质是更大的战斗服):战斗号 = max(WT2、WT4 的 Id)+1;
	// 副本服(WT3)单独一池。已分配号写入 used 防本批次内撞号。
	used := idSet(rows)
	battleIDs := make([]int, pairs)
	copyIDs := make([]int, pairs)
	for i := 0; i < pairs; i++ {
		bid, err := nextID(rows, used, 2) // 战斗服 = WT2 自身 max+1(不再与中心服共池)
		if err != nil {
			return nil, err
		}
		used[bid] = true
		battleIDs[i] = bid
		cid, err := nextID(rows, used, 3)
		if err != nil {
			return nil, err
		}
		used[cid] = true
		copyIDs[i] = cid
	}
	seqStart := nextRegionSeq(rows, p.RegionName)

	var out []PlannedRow
	// 先战斗对
	for i := 0; i < pairs; i++ {
		bm := battleMachines[i]
		bid := battleIDs[i]
		cid := copyIDs[i]

		brow := cloneRow(battleTpl)
		brow["Id"] = strconv.Itoa(bid)
		brow["RealWorldID"] = strconv.Itoa(bid) // 有效服恒等于自身 Id
		brow["WorldType"] = "2"
		brow["WorldName"] = "WorldWar" + strconv.Itoa(bid)
		brow["SelfPublicIp"] = bm.selfIp
		brow["RealSelfPublicIp"] = bm.realIp
		setPublicUrl(brow, p.AddPublicUrl)
		applyPortBand(brow, battleBase)
		brow["DataBaseName"] = "ptdb_" + strconv.Itoa(bid)
		brow["MySqlIp"] = p.BattleMySqlIp
		brow["BattleWorldID"] = strconv.Itoa(bid)
		brow["GlobalCenterWorldID"] = "-1"
		brow["BigWorldID"] = "-1"
		out = append(out, summarize("battle", bid, brow))

		crow := cloneRow(copyTpl)
		crow["Id"] = strconv.Itoa(cid)
		crow["RealWorldID"] = strconv.Itoa(cid) // 有效服恒等于自身 Id
		crow["WorldType"] = "3"
		crow["WorldName"] = "WorldCopyWar" + strconv.Itoa(cid)
		crow["SelfPublicIp"] = bm.selfIp
		crow["RealSelfPublicIp"] = bm.realIp
		setPublicUrl(crow, p.AddPublicUrl)
		applyPortBand(crow, copyBase)
		crow["DataBaseName"] = "ptdb_" + strconv.Itoa(cid)
		crow["MySqlIp"] = p.BattleMySqlIp
		crow["BattleWorldID"] = strconv.Itoa(bid)
		crow["GlobalCenterWorldID"] = "-1"
		crow["BigWorldID"] = "-1"
		out = append(out, summarize("copy", cid, crow))
	}

	// 中心服(可选,整批共用一个):挂现有 或 新建一个(WT4 + 中心副本 WT3)。
	var centerID int
	if p.ExistingCenterID > 0 {
		if !isCenterID(rows, p.ExistingCenterID) {
			return nil, fmt.Errorf("所选中心服 %d 不存在", p.ExistingCenterID)
		}
		centerID = p.ExistingCenterID
	} else if p.NewCenterMachine.selfIp != "" {
		if p.CenterMySqlIp == "" {
			return nil, fmt.Errorf("新建中心服需填写中心服 MySqlIp")
		}
		centerTpl := latestTemplate(rows, 4)
		copyTpl4 := latestTemplate(rows, 3)
		if centerTpl == nil || copyTpl4 == nil {
			return nil, fmt.Errorf("缺少中心服/副本服模板(WT4/WT3)")
		}
		cid, err := nextID(rows, used, 4) // 中心服 = WT4 自身 max+1(不再与战斗服共池)
		if err != nil {
			return nil, err
		}
		used[cid] = true
		centerID = cid

		ctrow := cloneRow(centerTpl)
		ctrow["Id"] = strconv.Itoa(cid)
		ctrow["RealWorldID"] = strconv.Itoa(cid)
		ctrow["WorldType"] = "4"
		ctrow["WorldName"] = "worldcenter" + strconv.Itoa(cid)
		ctrow["SelfPublicIp"] = p.NewCenterMachine.selfIp
		ctrow["RealSelfPublicIp"] = p.NewCenterMachine.realIp
		setPublicUrl(ctrow, p.AddPublicUrl)
		applyPortBand(ctrow, centerBase)
		ctrow["DataBaseName"] = "ptdb_" + strconv.Itoa(cid)
		ctrow["MySqlIp"] = p.CenterMySqlIp
		ctrow["GlobalCenterWorldID"] = strconv.Itoa(cid)
		ctrow["BattleWorldID"] = "-1"
		ctrow["BigWorldID"] = "-1"
		out = append(out, summarize("center", cid, ctrow))

		ccid, err := nextID(rows, used, 3)
		if err != nil {
			return nil, err
		}
		used[ccid] = true
		cprow := cloneRow(copyTpl4)
		cprow["Id"] = strconv.Itoa(ccid)
		cprow["RealWorldID"] = strconv.Itoa(ccid)
		cprow["WorldType"] = "3"
		cprow["WorldName"] = "WorldCopyCenter" + strconv.Itoa(ccid)
		cprow["SelfPublicIp"] = p.NewCenterMachine.selfIp
		cprow["RealSelfPublicIp"] = p.NewCenterMachine.realIp
		setPublicUrl(cprow, p.AddPublicUrl)
		applyPortBand(cprow, copyBase)
		cprow["DataBaseName"] = "ptdb_" + strconv.Itoa(ccid)
		cprow["MySqlIp"] = p.CenterMySqlIp
		cprow["GlobalCenterWorldID"] = strconv.Itoa(cid)
		cprow["BattleWorldID"] = "-1"
		cprow["BigWorldID"] = "-1"
		out = append(out, summarize("copy", ccid, cprow))
	}

	// 再游戏服
	for i := 0; i < p.Count; i++ {
		s := slots[i]
		gid := gameIDs[i]
		bid := battleIDs[i/4]

		grow := cloneRow(gameTpl)
		seq := seqStart + 1 + i
		grow["Id"] = strconv.Itoa(gid)
		grow["RealWorldID"] = strconv.Itoa(gid) // 有效服恒等于自身 Id
		grow["WorldType"] = "0"
		grow["WorldName"] = letter + strconv.Itoa(seq)                  // 短码:字母前缀+序号(进 server 文件)
		grow["ServerShowName"] = p.RegionName + strconv.Itoa(seq) + "服" // 中文展示名(进 client 文件)
		grow["Desc"] = grow["ServerShowName"]                           // Desc 直接等于 ServerShowName
		grow["WoRegion"] = p.RegionName                                 // 大区自维护列(与单服创建对齐,分组才正确)
		grow["SelfPublicIp"] = s.selfIp
		grow["RealSelfPublicIp"] = s.realIp
		setPublicUrl(grow, p.AddPublicUrl)
		applyPortBand(grow, s.base)
		grow["DataBaseName"] = "ptdb_" + strconv.Itoa(gid)
		grow["MySqlIp"] = p.GameMySqlIp
		grow["BattleWorldID"] = strconv.Itoa(bid)
		if centerID > 0 {
			grow["GlobalCenterWorldID"] = strconv.Itoa(centerID)
		}
		out = append(out, summarize("game", gid, grow))
	}
	return &NewServerPlan{Rows: out}, nil
}

func cloneRow(r map[string]string) map[string]string {
	out := make(map[string]string, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

func summarize(kind string, id int, fields map[string]string) PlannedRow {
	return PlannedRow{
		Kind: kind, ID: id, Fields: fields,
		WorldName: fields["WorldName"], ServerShowName: fields["ServerShowName"],
		SelfPublicIp: fields["SelfPublicIp"],
		PortBase:     portBaseOf(fields), BattleWorldID: atoiField(fields, "BattleWorldID"),
		DataBaseName: fields["DataBaseName"], MySqlIp: fields["MySqlIp"],
	}
}

// ExistingRegions 导出供 handler 读现有大区列表。
func ExistingRegions(rows []map[string]string) []string { return existingRegions(rows) }
