package gameserver

import (
	"bufio"
	"bytes"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Server 一个游戏服的精简信息(供下拉选择)。
type Server struct {
	ID            int
	Desc          string
	Name          string
	IP            string // 内网IP(SelfPublicIp,执行器 SSH 用)
	OutIP         string // 外网IP(RealSelfPublicIp,对客户端的真实地址;仅展示)
	WorldType     int
	RealWorldID   int // 正常服==自身 Id;合服废弃服(WorldType=-1)==合入的目标服 Id
	BattleWorldID int
}

// MergedSource 已合入某目标服的源服(合服废弃服)精简信息。
type MergedSource struct {
	ID   int
	Desc string
}

// ServerGroup 按区服名前缀分组后的服务器集合。
type ServerGroup struct {
	Group   string   `json:"group"`
	Servers []Server `json:"servers"`
}

// Source 提供工单系统所需的服务器数据(实现见 gsconfig)。
type Source interface {
	Servers() ([]Server, error)       // 全部服(不含凭据)
	DBConns() (map[int]DBConn, error) // 服ID -> MySQL 连接(仅执行器内部用)
}

// 列索引(对应 ServerConfigList.txt)
const (
	colID            = 0
	colDesc          = 1
	colName          = 2
	colWorldType     = 4
	colRealWorldID   = 6
	colBattleWorldID = 7
	colIP            = 9
)

var (
	// parenRe 匹配中英文括号及其内容(用于去掉 "(正式服)" 之类的标签)。
	parenRe = regexp.MustCompile(`[\(（][^\)）]*[\)）]`)
	// tailNumRe 匹配结尾的 "数字" 或 "数字+服"(用于去掉 "73服" 得到区服名前缀)。
	tailNumRe = regexp.MustCompile(`[0-9]+服?$`)
)

// readDecoded 读取文件;若内容不是合法 UTF-8 则按 GBK 解码,统一返回 UTF-8 字节。
func readDecoded(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if utf8.Valid(raw) {
		return raw, nil
	}
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ParseFile 解析配置文件,跳过前 4 行表头与以 # 开头的行。文件为 GBK 时自动转 UTF-8。
func ParseFile(path string) ([]Server, error) {
	data, err := readDecoded(path)
	if err != nil {
		return nil, err
	}

	var servers []Server
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.ReplaceAll(scanner.Text(), "\r", "")
		if lineNum <= 4 || strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) <= colIP {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(fields[colID]))
		if err != nil {
			continue
		}
		wt, _ := strconv.Atoi(strings.TrimSpace(fields[colWorldType]))
		rwid, _ := strconv.Atoi(strings.TrimSpace(fields[colRealWorldID]))
		bwid, _ := strconv.Atoi(strings.TrimSpace(fields[colBattleWorldID]))
		servers = append(servers, Server{
			ID:            id,
			Desc:          strings.TrimSpace(fields[colDesc]),
			Name:          strings.TrimSpace(fields[colName]),
			IP:            strings.TrimSpace(fields[colIP]),
			WorldType:     wt,
			RealWorldID:   rwid,
			BattleWorldID: bwid,
		})
	}
	return servers, scanner.Err()
}

// worldTypeNames WorldType -> 中文名(以 ServerConfigList 列说明为准)。其余回退为 "类型N"。
var worldTypeNames = map[int]string{
	-1: "合服废弃",
	0:  "普通世界",
	1:  "大世界",
	2:  "战场服",
	3:  "战场副本服",
	4:  "中心服",
}

// WorldTypeName 返回世界类型的中文名;未知类型回退 "类型N"(不臆造)。
func WorldTypeName(wt int) string {
	if n, ok := worldTypeNames[wt]; ok {
		return n
	}
	return "类型" + strconv.Itoa(wt)
}

// MergedSources 扫描全部服,返回 目标服Id -> 已合入的源服列表。
// 合服废弃服(WorldType==-1)按其 RealWorldID 归属到目标服;列表按源服 ID 升序。
// 目标无效(RealWorldID<=0 或指向自身)的脏数据忽略,不归并。
func MergedSources(servers []Server) map[int][]MergedSource {
	out := make(map[int][]MergedSource)
	for _, s := range servers {
		if s.WorldType != -1 {
			continue
		}
		target := s.RealWorldID
		if target <= 0 || target == s.ID {
			continue
		}
		out[target] = append(out[target], MergedSource{ID: s.ID, Desc: s.Desc})
	}
	for t := range out {
		sort.Slice(out[t], func(i, j int) bool { return out[t][i].ID < out[t][j].ID })
	}
	return out
}

// GroupName 从服务器描述(Desc)提取区服名前缀作为分组名。
// 规则:去掉括号标签 (...),再去掉结尾的 "数字" 或 "数字+服",最后去掉单独的 "服"。
// 例:"洛阳73服"->"洛阳";"流云洲1服(正式服)"->"流云洲";"战斗副本服"->"战斗副本";"压测1服"->"压测"。
func GroupName(desc string) string {
	s := parenRe.ReplaceAllString(desc, "")
	s = strings.TrimSpace(s)
	s = tailNumRe.ReplaceAllString(s, "")
	s = strings.TrimSuffix(s, "服")
	s = strings.TrimSpace(s)
	if s == "" {
		return "其他"
	}
	return s
}

// BattleGroupOf 返回某游戏服对应的 战斗服ID 与 战斗副本服ID(参考 merge.sh):
//
//	battleID = 该游戏服的 BattleWorldID 字段值(即所属战斗服的 worldid)
//	copyID   = WorldType==3 且 BattleWorldID==battleID 的那台战斗副本服
//
// 多个游戏服可能共用同一组战斗服/副本服。找不到的返回 -1。
func BattleGroupOf(servers []Server, gameServerID int) (battleID, copyID int) {
	bid := -1
	for _, s := range servers {
		if s.ID == gameServerID {
			bid = s.BattleWorldID
			break
		}
	}
	if bid <= 0 {
		return -1, -1
	}
	copyID = -1
	for _, s := range servers {
		if s.WorldType == 3 && s.BattleWorldID == bid {
			copyID = s.ID
			break
		}
	}
	return bid, copyID
}

// ExpandWithBattle 给定一组游戏服ID,补上各自的战斗服与战斗副本服(去重),
// 返回需要一并操作(如停服/启动)的完整服ID列表;保持首次出现顺序。
func ExpandWithBattle(servers []Server, gameServerIDs []int) []int {
	seen := make(map[int]bool)
	var out []int
	add := func(id int) {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range gameServerIDs {
		add(id)
		b, c := BattleGroupOf(servers, id)
		add(b)
		add(c)
	}
	return out
}

// DBConn 一个服的 MySQL 连接信息(仅供执行器查版本/升级用,绝不进 Server 结构体或前端)。
type DBConn struct {
	IP   string
	Port string
	User string
	Pwd  string
}

// BuildDBConnMap 解析 ServerConfigList.txt,按表头名定位 MySQL 连接列,
// 返回 服ID -> DBConn。表头识别失败或某列缺失则跳过该服。
func BuildDBConnMap(path string) (map[int]DBConn, error) {
	data, err := readDecoded(path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)

	col := map[string]int{}
	lineNum := 0
	out := make(map[int]DBConn)
	for scanner.Scan() {
		lineNum++
		line := strings.ReplaceAll(scanner.Text(), "\r", "")
		fields := strings.Split(line, "\t")
		if lineNum == 1 {
			for i, name := range fields {
				col[strings.TrimSpace(name)] = i
			}
			continue
		}
		if lineNum <= 4 || strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(fields) {
				return ""
			}
			return strings.TrimSpace(fields[i])
		}
		id, err := strconv.Atoi(get("Id"))
		if err != nil {
			continue
		}
		out[id] = DBConn{
			IP:   get("MySqlIp"),
			Port: get("MySqlPort"),
			User: get("DataBaseUser"),
			Pwd:  get("DataBasePsw"),
		}
	}
	return out, scanner.Err()
}

// GroupServers 按区服名前缀分组(纯函数):过滤默认 Id>10000 且 WorldType∈{0,2,3};
// showAll=true 去掉 Id 限制(仍保留 WorldType 过滤)。组内按 ID 升序,组间按组内最小 ID 升序。
func GroupServers(servers []Server, showAll bool) []ServerGroup {
	groupMap := make(map[string][]Server)
	for _, s := range servers {
		if !showAll && s.ID <= 10000 {
			continue
		}
		if s.WorldType != 0 && s.WorldType != 2 && s.WorldType != 3 {
			continue
		}
		g := GroupName(s.Desc)
		groupMap[g] = append(groupMap[g], s)
	}
	groups := make([]ServerGroup, 0, len(groupMap))
	for g, ss := range groupMap {
		sort.Slice(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
		groups = append(groups, ServerGroup{Group: g, Servers: ss})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Servers[0].ID < groups[j].Servers[0].ID
	})
	return groups
}
