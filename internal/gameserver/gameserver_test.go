package gameserver

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestWorldTypeName(t *testing.T) {
	cases := map[int]string{
		-1: "合服废弃",
		0:  "普通世界",
		1:  "大世界",
		2:  "战场服",
		3:  "战场副本服",
		4:  "中心服",
		99: "类型99", // 未知类型回退,不臆造名称
	}
	for wt, want := range cases {
		if got := WorldTypeName(wt); got != want {
			t.Errorf("WorldTypeName(%d) = %q, want %q", wt, got, want)
		}
	}
}

func TestParseFile(t *testing.T) {
	servers, err := ParseFile("../../testdata/ServerConfigList.txt")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("len = %d, want 3", len(servers))
	}
	if servers[0].ID != 210 {
		t.Errorf("id = %d, want 210", servers[0].ID)
	}
	if servers[0].IP != "192.168.10.163" {
		t.Errorf("ip = %q", servers[0].IP)
	}
	if servers[0].Desc != "210区-推荐服" {
		t.Errorf("desc = %q", servers[0].Desc)
	}
}

// sclRowMerged 拼一个合服废弃服行:WorldType=-1、RealWorldID=目标服。
func sclRowMerged(id, target int, desc, ip string) string {
	return fmt.Sprintf("%d\t%s\tW\t\t-1\t-1\t%d\t-1\t-1\t%s\n", id, desc, target, ip)
}

func TestParseFileReadsRealWorldID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	// 正常服 RealWorldID==自身;合服废弃服 RealWorldID==目标服。
	content := scl(
		sclRow(235, 0, "235世界", "1.1.1.1") +
			sclRowMerged(10000, 235, "drc", "1.1.1.2"))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	servers, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if servers[0].ID != 235 || servers[0].RealWorldID != 235 {
		t.Errorf("正常服 RealWorldID 应==自身, got %+v", servers[0])
	}
	if servers[1].ID != 10000 || servers[1].WorldType != -1 || servers[1].RealWorldID != 235 {
		t.Errorf("废弃服应 WorldType=-1/RealWorldID=235, got %+v", servers[1])
	}
}

func TestMergedSources(t *testing.T) {
	servers := []Server{
		{ID: 235, Desc: "235世界", WorldType: 0, RealWorldID: 235},
		{ID: 10000, Desc: "drc", WorldType: -1, RealWorldID: 235},
		{ID: 10001, Desc: "老二服", WorldType: -1, RealWorldID: 235},
		{ID: 9000, Desc: "旧服", WorldType: -1, RealWorldID: 300}, // 合入另一目标
		{ID: 8000, Desc: "脏数据", WorldType: -1, RealWorldID: -1}, // 无有效目标,忽略
		{ID: 12801, Desc: "战斗服", WorldType: 2, RealWorldID: 12801},
	}
	m := MergedSources(servers)
	got := m[235]
	if len(got) != 2 {
		t.Fatalf("235 应合入 2 个源服, got %+v", got)
	}
	if got[0].ID != 10000 || got[1].ID != 10001 { // 按 ID 升序
		t.Errorf("源服应按 ID 升序, got %+v", got)
	}
	if got[0].Desc != "drc" {
		t.Errorf("源服应带区服名, got %+v", got[0])
	}
	if len(m[300]) != 1 || m[300][0].ID != 9000 {
		t.Errorf("300 应合入 9000, got %+v", m[300])
	}
	if _, ok := m[-1]; ok {
		t.Errorf("无有效目标的脏数据不应归并: %+v", m)
	}
	if len(m) != 2 { // 仅 235、300 两个目标
		t.Errorf("目标数应为 2, got %d: %+v", len(m), m)
	}
}

func TestGroupName(t *testing.T) {
	cases := map[string]string{
		"洛阳73服":       "洛阳",
		"流云洲1服(正式服)": "流云洲",
		"战斗副本服":       "战斗副本",
		"战斗服":         "战斗",
		"压测1服":        "压测",
		"新春迎新139服":    "新春迎新",
		"望海城1服(正式服)":  "望海城",
	}
	for in, want := range cases {
		if got := GroupName(in); got != want {
			t.Errorf("GroupName(%q) = %q, want %q", in, got, want)
		}
	}
}

// scl 拼一个最小的 ServerConfigList(4 行表头 + 数据行,\t 分隔到 SelfPublicIp 列)。
func scl(rows string) string {
	header := "Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tc7\tc8\tSelfPublicIp\n" +
		"INT\tline2\n" + "config\tline3\n" + "#ID\tline4\n"
	return header + rows
}

func sclRow(id, wt int, desc, ip string) string {
	return fmt.Sprintf("%d\t%s\tW\t\t%d\t-1\t%d\t-1\t-1\t%s\n", id, desc, wt, id, ip)
}

// sclRowB 像 sclRow 但可指定 BattleWorldID(col8,索引7)。
func sclRowB(id, wt, battle int, desc, ip string) string {
	return fmt.Sprintf("%d\t%s\tW\t\t%d\t-1\t%d\t%d\t-1\t%s\n", id, desc, wt, id, battle, ip)
}

func battleFixture(t *testing.T) []Server {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	content := scl(
		sclRowB(10001, 0, 12801, "望海城1服", "1.1.1.1") + // 普通服 -> battle 12801
			sclRowB(10002, 0, 12801, "望海城2服", "1.1.1.2") + // 普通服 -> 同一 battle
			sclRowB(12801, 2, 12801, "战斗服", "2.2.2.1") + // 战斗服(WT=2, Id=battle)
			sclRowB(12901, 3, 12801, "战斗副本服", "2.2.2.2") + // 副本服(WT=3, BattleWorldID=battle)
			sclRowB(10003, 0, -1, "孤立服", "1.1.1.3")) // 无战斗服
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	servers, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func TestBattleGroupOf(t *testing.T) {
	servers := battleFixture(t)

	b, c := BattleGroupOf(servers, 10001)
	if b != 12801 || c != 12901 {
		t.Errorf("10001 -> battle=%d copy=%d, want 12801/12901", b, c)
	}
	// 共用同一组
	b2, c2 := BattleGroupOf(servers, 10002)
	if b2 != 12801 || c2 != 12901 {
		t.Errorf("10002 应共用同组, got battle=%d copy=%d", b2, c2)
	}
	// 无战斗服
	b3, c3 := BattleGroupOf(servers, 10003)
	if b3 != -1 || c3 != -1 {
		t.Errorf("孤立服应返回 -1/-1, got %d/%d", b3, c3)
	}
}

func TestExpandWithBattle(t *testing.T) {
	servers := battleFixture(t)
	got := ExpandWithBattle(servers, []int{10001, 10002})
	want := []int{10001, 12801, 12901, 10002} // 战斗/副本去重,只出现一次
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseFileGBK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gbk.txt")
	utf8Content := scl(sclRow(10142, 0, "洛阳73服", "3.3.3.3"))
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(utf8Content))
	if err != nil {
		t.Fatalf("编码 GBK: %v", err)
	}
	if err := os.WriteFile(path, gbk, 0644); err != nil {
		t.Fatal(err)
	}

	servers, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(servers) != 1 || servers[0].Desc != "洛阳73服" {
		t.Fatalf("GBK 解码失败: %+v", servers)
	}
}

func TestBuildDBConnMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	header := "Id\tDesc\tWorldName\tServerShowName\tWorldType\tBigWorldID\tRealWorldID\tBattleWorldID\tGlobalCenterWorldID\tSelfPublicIp\tSelfPublicIpv6\tRealSelfPublicIp\tRealSelfPublicUrl\tRealSelfPublicIpv6\tPortForClient\tRealPortForClient\tRealPortForClientUrl\tPortForGMServer\tServerThreadCount\tDBThreadCount\tHttpThreadCount\tDBIP\tDBPort\tHttpIP\tHttpPort\tHttpServerCommonPort\tPortForBigWorld\tPortForBattleWorld\tPortForBattleCopySceneWorld\tPortForGlobalCenter\tDBRedisIP\tDBRedisPort\tDataBaseName\tMySqlIp\tMySqlPort\tDataBaseUser\tDataBasePsw\n"
	row := "10001\tD\tW\tS\t0\t-1\t10001\t-1\t-1\t10.0.0.1\t-\t-\t-\t-\t0\t0\t-\t0\t1\t1\t1\t127.0.0.1\t1\t-\t0\t0\t0\t0\t0\t0\t-\t0\tptdb_10001\t10.0.0.9\t3306\tu1\tp1\n"
	content := header + "INT\t-\nconfig\t-\n#ID\t-\n" + row
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := BuildDBConnMap(path)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := m[10001]
	if !ok {
		t.Fatalf("应含服 10001, got %v", m)
	}
	if c.IP != "10.0.0.9" || c.Port != "3306" || c.User != "u1" || c.Pwd != "p1" {
		t.Errorf("DBConn = %+v, want {10.0.0.9 3306 u1 p1}", c)
	}
}

func TestServerStructHasNoDBCreds(t *testing.T) {
	tp := reflect.TypeOf(Server{})
	for i := 0; i < tp.NumField(); i++ {
		name := strings.ToLower(tp.Field(i).Name)
		if strings.Contains(name, "pwd") || strings.Contains(name, "passw") || strings.Contains(name, "mysql") || strings.Contains(name, "dbuser") {
			t.Errorf("Server 不应含 DB 凭据字段: %s", tp.Field(i).Name)
		}
	}
}

func TestGroupServers(t *testing.T) {
	servers := []Server{
		{ID: 10001, Desc: "洛阳1服", WorldType: 0},
		{ID: 10002, Desc: "洛阳2服", WorldType: 0},
		{ID: 9000, Desc: "内部服", WorldType: 0},   // Id<=10000,默认过滤
		{ID: 10003, Desc: "压测1服", WorldType: 5}, // WorldType 不在 {0,2,3},过滤
	}
	groups := GroupServers(servers, false)
	if len(groups) != 1 {
		t.Fatalf("组数 = %d, want 1(只剩洛阳组)", len(groups))
	}
	if groups[0].Group != "洛阳" || len(groups[0].Servers) != 2 {
		t.Errorf("分组错: %+v", groups[0])
	}
	all := GroupServers(servers, true)
	total := 0
	for _, g := range all {
		total += len(g.Servers)
	}
	if total != 3 {
		t.Errorf("showAll 服总数 = %d, want 3(10001/10002/9000;10003 仍因 WorldType 被过滤)", total)
	}
}

func TestGroupServersIsSourceConsumer(t *testing.T) {
	var _ Source = staticTestSource{} // 编译期确认 Source 接口签名
}

type staticTestSource struct{}

func (staticTestSource) Servers() ([]Server, error)       { return nil, nil }
func (staticTestSource) DBConns() (map[int]DBConn, error) { return nil, nil }
