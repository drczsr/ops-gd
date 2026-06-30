package gameserver

import (
	"fmt"
	"sort"
	"strconv"
)

// GameSlot 一个游戏服可落点:机器两 IP + 空闲端口档 base。
type GameSlot struct {
	SelfPublicIp     string
	RealSelfPublicIp string
	PortBase         int
}

// BattleOption 可选的已有战斗服(WT2)及其当前挂接游戏服数。
type BattleOption struct {
	ID            int
	AttachedGames int
	SelfPublicIp  string
}

// CenterOption 可选的已有中心服(WT4)及其挂载的战斗副本服(WT3)。
type CenterOption struct {
	ID           int
	SelfPublicIp string
	CopyIDs      []int // 挂载的全部战斗副本服(WT3 且 GlobalCenterWorldID==ID)Id,按 Id 升序;无则空
}

// SelfIp/RealIp 暴露 machine 的内/外网 IP(供 handler/模板用)。
func (m machine) SelfIp() string { return m.selfIp }
func (m machine) RealIp() string { return m.realIp }

// AvailableGameSlots 列出全部游戏服可落点(机器 + 空闲端口档),复用 pickGameSlots 的机器/档位口径。
// 返回全部空闲槽(不截断),机器按 SelfIp 升序、档位按 gameBands 顺序。
func AvailableGameSlots(rows []map[string]string) []GameSlot {
	type mach struct {
		realIp string
		used   map[int]bool
	}
	machines := map[string]*mach{}
	var order []string
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
			order = append(order, sip)
		}
		if m.realIp == "" {
			m.realIp = r["RealSelfPublicIp"]
		}
		// 占档只看在用游戏服(WT0),与 Id 无关:低 Id 老服(如 235)同样占其端口档。
		if isGame {
			m.used[portBaseOf(r)] = true
		}
	}
	sort.Strings(order)
	var out []GameSlot
	for _, sip := range order {
		m := machines[sip]
		for _, b := range gameBands {
			if !m.used[b] {
				out = append(out, GameSlot{SelfPublicIp: sip, RealSelfPublicIp: m.realIp, PortBase: b})
			}
		}
	}
	return out
}

// BattleServersWithCapacity 列出挂接游戏服 <4 的现有战斗服(WT2),按 ID 升序。
func BattleServersWithCapacity(rows []map[string]string) []BattleOption {
	attached := map[int]int{}
	for _, r := range rows {
		if atoiField(r, "WorldType") == 0 {
			if b := atoiField(r, "BattleWorldID"); b > 0 {
				attached[b]++
			}
		}
	}
	var out []BattleOption
	for _, r := range rows {
		if atoiField(r, "WorldType") != 2 {
			continue
		}
		id := atoiField(r, "Id")
		n := attached[id]
		if n < 4 {
			out = append(out, BattleOption{ID: id, AttachedGames: n, SelfPublicIp: r["SelfPublicIp"]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// FreeBattleMachinesFor 导出空闲战斗机器列表(供 UI 选机新建战斗服)。
func FreeBattleMachinesFor(rows []map[string]string) []machine {
	return freeBattleMachines(rows)
}

// CenterServers 列出全部中心服(WT4)及各自挂载的全部战斗副本服(WT3 且 GlobalCenterWorldID==中心Id),按 ID 升序。
func CenterServers(rows []map[string]string) []CenterOption {
	// 先建中心Id -> 副本Id列表的映射(战斗副本 GlobalCenterWorldID=-1,不会落入此映射)。
	copiesOf := map[int][]int{}
	for _, r := range rows {
		if atoiField(r, "WorldType") != 3 {
			continue
		}
		gc := atoiField(r, "GlobalCenterWorldID")
		if gc <= 0 {
			continue
		}
		copiesOf[gc] = append(copiesOf[gc], atoiField(r, "Id"))
	}
	for _, ids := range copiesOf {
		sort.Ints(ids)
	}
	var out []CenterOption
	for _, r := range rows {
		if atoiField(r, "WorldType") != 4 {
			continue
		}
		id := atoiField(r, "Id")
		out = append(out, CenterOption{ID: id, SelfPublicIp: r["SelfPublicIp"], CopyIDs: copiesOf[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CreateGameParams 创建单个游戏服的输入(向导收集)。
type CreateGameParams struct {
	ServerName   string   // 服务器展示名(→ ServerShowName/Desc),手填
	WorldName    string   // WorldName 短码(→ 服务器端配置),手填
	Slot         GameSlot // PortBase>0=下拉选定档;PortBase==0=手填IP,自动取该机最小空闲档
	GameMySqlIp  string
	AddPublicUrl bool // 是否添加外网域名地址:true=wss://<gs-tw|ws-tw>.../s<id>;false=填外网IP

	BattleEnabled    bool // 是否配置战斗服;false=不挂战斗服(BattleWorldID=-1)
	ExistingBattleID int
	NewBattleMachine machine
	BattleMySqlIp    string

	ExistingCenterID int
	NewCenterMachine machine
	CenterMySqlIp    string

	// 手填起始号(0=未填,由系统按 max+1 发):某类型库里一个都没有(无发号基准)时,
	// Plan 返回 Missing 让前端弹框补填,带着这些值再次提交即用手填值发第一个。
	ManualGameID       int
	ManualBattleID     int
	ManualCopyID       int
	ManualCenterID     int
	ManualCenterCopyID int
}

// CreatePlan 创建方案:1 游戏服行 + (可选)1 战斗 + 1 副本 + (可选)1 中心服。
type CreatePlan struct {
	Rows []PlannedRow
}

// MachineFrom 由内外网 IP 构造 machine(供 handler 组装新建战斗/中心落机)。
func MachineFrom(selfIp, realIp string) machine { return machine{selfIp: selfIp, realIp: realIp} }

// worldNameExists rows 中是否已有该 WorldName(短码须唯一,手填创建时校验)。
func worldNameExists(rows []map[string]string, name string) bool {
	for _, r := range rows {
		if r["WorldName"] == name {
			return true
		}
	}
	return false
}

// freeBandFor 返回机器 selfIp 上最小的空闲游戏端口档(3300/3400/3500);三档全被现有游戏服(WT0)占用则 ok=false。
// 手填落点IP时用它校验+定档:有空闲档才能落,否则报错。
func freeBandFor(rows []map[string]string, selfIp string) (int, bool) {
	usedBand := map[int]bool{}
	for _, r := range rows {
		if r["SelfPublicIp"] == selfIp && atoiField(r, "WorldType") == 0 {
			usedBand[portBaseOf(r)] = true
		}
	}
	for _, b := range gameBands { // {3300, 3400, 3500}
		if !usedBand[b] {
			return b, true
		}
	}
	return 0, false
}

const centerBase = 3500 // 中心服端口档(与现网 297/397/497 一致)

// PlanCreateGameServer 算出创建单个游戏服的待插入行(游戏 + 可选新建战斗/副本/中心)。
func PlanCreateGameServer(rows []map[string]string, p CreateGameParams) (*CreatePlan, error) {
	if p.ServerName == "" {
		return nil, fmt.Errorf("服务器名称不能为空")
	}
	if p.WorldName == "" {
		return nil, fmt.Errorf("WorldName 短码不能为空")
	}
	if worldNameExists(rows, p.WorldName) {
		return nil, fmt.Errorf("WorldName 短码 %q 已存在", p.WorldName)
	}
	if p.Slot.SelfPublicIp == "" {
		return nil, fmt.Errorf("请选择落点或填写机器IP")
	}
	if p.GameMySqlIp == "" {
		return nil, fmt.Errorf("请填写游戏服 MySqlIp")
	}
	gameTpl := latestTemplate(rows, 0)
	if gameTpl == nil {
		return nil, fmt.Errorf("缺少游戏服模板(WT0)")
	}
	used := idSet(rows)

	// 端口档:下拉给了具体档(PortBase>0)直接用;手填IP(PortBase==0)自动取该机最小空闲档,全占则报错。
	slotBase := p.Slot.PortBase
	if slotBase == 0 {
		b, ok := freeBandFor(rows, p.Slot.SelfPublicIp)
		if !ok {
			return nil, fmt.Errorf("机器 %s 的端口档(3300/3400/3500)已全部占用,无空闲落点", p.Slot.SelfPublicIp)
		}
		slotBase = b
	}

	var out []PlannedRow

	// 1) 战斗服 Id(仅在勾选「配置战斗服」时挂/建;否则 -1=不挂战斗服)
	battleID := -1
	if p.BattleEnabled && p.ExistingBattleID > 0 {
		battleID = p.ExistingBattleID
	} else if p.BattleEnabled {
		if p.NewBattleMachine.selfIp == "" {
			return nil, fmt.Errorf("新建战斗服需指定落机")
		}
		if p.BattleMySqlIp == "" {
			return nil, fmt.Errorf("新建战斗服需填写战斗服 MySqlIp")
		}
		battleTpl := latestTemplate(rows, 2)
		copyTpl := latestTemplate(rows, 3)
		if battleTpl == nil || copyTpl == nil {
			return nil, fmt.Errorf("缺少战斗服/副本服模板(WT2/WT3)")
		}
		// 战斗服 = WT2 自己的 max+1;副本 = WT3 max+1(手填值优先)。
		bid, err := pickID(p.ManualBattleID, rows, used, 2)
		if err != nil {
			return nil, fmt.Errorf("战斗服%w", err)
		}
		used[bid] = true
		cid, err := pickID(p.ManualCopyID, rows, used, 3)
		if err != nil {
			return nil, fmt.Errorf("战斗副本服%w", err)
		}
		used[cid] = true
		battleID = bid

		brow := cloneRow(battleTpl)
		brow["Id"] = strconv.Itoa(bid)
		brow["RealWorldID"] = strconv.Itoa(bid)
		brow["WorldType"] = "2"
		brow["WorldName"] = "WorldWar" + strconv.Itoa(bid)
		brow["SelfPublicIp"] = p.NewBattleMachine.selfIp
		brow["RealSelfPublicIp"] = p.NewBattleMachine.realIp
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
		crow["RealWorldID"] = strconv.Itoa(cid)
		crow["WorldType"] = "3"
		crow["WorldName"] = "WorldCopyWar" + strconv.Itoa(cid)
		crow["SelfPublicIp"] = p.NewBattleMachine.selfIp
		crow["RealSelfPublicIp"] = p.NewBattleMachine.realIp
		setPublicUrl(crow, p.AddPublicUrl)
		applyPortBand(crow, copyBase)
		crow["DataBaseName"] = "ptdb_" + strconv.Itoa(cid)
		crow["MySqlIp"] = p.BattleMySqlIp
		crow["BattleWorldID"] = strconv.Itoa(bid)
		crow["GlobalCenterWorldID"] = "-1"
		crow["BigWorldID"] = "-1"
		out = append(out, summarize("copy", cid, crow))
	}

	// 2) 中心服 Id
	var centerID int
	if p.ExistingCenterID > 0 {
		centerID = p.ExistingCenterID
	} else if p.NewCenterMachine.selfIp != "" {
		if p.CenterMySqlIp == "" {
			return nil, fmt.Errorf("新建中心服需填写中心服 MySqlIp")
		}
		centerTpl := latestTemplate(rows, 4)
		if centerTpl == nil {
			return nil, fmt.Errorf("缺少中心服模板(WT4)")
		}
		copyTpl := latestTemplate(rows, 3)
		if copyTpl == nil {
			return nil, fmt.Errorf("缺少副本服模板(WT3)")
		}
		// 中心服 = WT4 自己的 max+1;中心副本 = WT3 max+1(手填值优先)。
		cid, err := pickID(p.ManualCenterID, rows, used, 4)
		if err != nil {
			return nil, fmt.Errorf("中心服%w", err)
		}
		used[cid] = true
		ccid, err := pickID(p.ManualCenterCopyID, rows, used, 3)
		if err != nil {
			return nil, fmt.Errorf("中心副本服%w", err)
		}
		used[ccid] = true
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

		// 中心战斗副本服(WT3,挂到中心服)
		cprow := cloneRow(copyTpl)
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

	// 3) 游戏服 Id = WT0 自己的 max+1(永远在已有最大值上递增,不回退到 10000;手填值优先)。
	id, err := pickID(p.ManualGameID, rows, used, 0)
	if err != nil {
		return nil, fmt.Errorf("游戏服%w", err)
	}
	used[id] = true

	grow := cloneRow(gameTpl)
	grow["Id"] = strconv.Itoa(id)
	grow["RealWorldID"] = strconv.Itoa(id)
	grow["WorldType"] = "0"
	grow["WorldName"] = p.WorldName       // 短码,手填
	grow["ServerShowName"] = p.ServerName // 展示名,手填
	grow["Desc"] = p.ServerName
	grow["WoRegion"] = "" // 单服创建不分组(清掉模板里带的值)
	grow["SelfPublicIp"] = p.Slot.SelfPublicIp
	grow["RealSelfPublicIp"] = p.Slot.RealSelfPublicIp
	setPublicUrl(grow, p.AddPublicUrl)
	applyPortBand(grow, slotBase)
	grow["DataBaseName"] = "ptdb_" + strconv.Itoa(id)
	grow["MySqlIp"] = p.GameMySqlIp
	grow["BattleWorldID"] = strconv.Itoa(battleID)
	if centerID > 0 {
		grow["GlobalCenterWorldID"] = strconv.Itoa(centerID)
	}
	out = append(out, summarize("game", id, grow))

	return &CreatePlan{Rows: out}, nil
}
