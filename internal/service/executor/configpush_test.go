package executor

import (
	"fmt"
	"strings"
	"testing"

	coslib "gongdan/internal/gsconfig/cos"
	"gongdan/internal/model"
)

type stubCOS struct {
	headVer  string
	headErr  error
	uploaded map[string][]byte
	putVer   string
}

func (s *stubCOS) Upload(key string, data []byte) (string, error) {
	if s.uploaded == nil {
		s.uploaded = map[string][]byte{}
	}
	s.uploaded[key] = data
	return s.putVer, nil
}
func (s *stubCOS) Download(key string, v ...string) ([]byte, error) { return nil, nil }
func (s *stubCOS) HeadVersionID(key string) (string, error) {
	if s.headErr != nil {
		return "", s.headErr
	}
	return s.headVer, nil
}

type stubArtifacts struct {
	art      ConfigArtifact
	deployed string
}

func (s *stubArtifacts) GetArtifact(id uint) (ConfigArtifact, error) { return s.art, nil }
func (s *stubArtifacts) SetDeployedVersion(id uint, v string) error  { s.deployed = v; return nil }

func TestConfigPushExecuteOK(t *testing.T) {
	cos := &stubCOS{headVer: "v1", putVer: "v2"}
	store := &stubArtifacts{art: ConfigArtifact{
		PrimaryKey:        "server/ServerConfigList.txt",
		SnapshotFiles:     map[string][]byte{"server/ServerConfigList.txt": []byte("DATA")},
		BaselineVersionID: "v1",
	}}
	e := &ConfigPushExecutor{cos: cos, store: store,
		pushAll: func(LogFunc) ([]int, error) { return nil, nil },
		gm:      func(LogFunc) {}}
	var logs []string
	err := e.Execute(&model.WorkOrder{ID: 7, Type: model.TypeConfigPush},
		func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(cos.uploaded["server/ServerConfigList.txt"]) != "DATA" {
		t.Fatal("未上传快照")
	}
	if store.deployed != "v2" {
		t.Fatalf("deployed = %q, want v2", store.deployed)
	}
}

func TestConfigPushBaselineStale(t *testing.T) {
	cos := &stubCOS{headVer: "vX", putVer: "v2"}
	store := &stubArtifacts{art: ConfigArtifact{
		PrimaryKey: "k", SnapshotFiles: map[string][]byte{"k": []byte("D")}, BaselineVersionID: "v1"}}
	e := &ConfigPushExecutor{cos: cos, store: store,
		pushAll: func(LogFunc) ([]int, error) { return nil, nil }, gm: func(LogFunc) {}}
	err := e.Execute(&model.WorkOrder{ID: 1}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "基准已过期") {
		t.Fatalf("应因基准过期失败, got %v", err)
	}
	if len(cos.uploaded) != 0 {
		t.Fatal("基准过期不应上传")
	}
}

func TestConfigPushPartialFail(t *testing.T) {
	cos := &stubCOS{headVer: "v1", putVer: "v2"}
	store := &stubArtifacts{art: ConfigArtifact{
		PrimaryKey: "k", SnapshotFiles: map[string][]byte{"k": []byte("D")}, BaselineVersionID: "v1"}}
	e := &ConfigPushExecutor{cos: cos, store: store,
		pushAll: func(LogFunc) ([]int, error) { return []int{1001}, nil }, gm: func(LogFunc) {}}
	err := e.Execute(&model.WorkOrder{ID: 1}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "1001") {
		t.Fatalf("应报失败服, got %v", err)
	}
}

func TestConfigPushPushAllError(t *testing.T) {
	cos := &stubCOS{headVer: "v1", putVer: "v2"}
	store := &stubArtifacts{art: ConfigArtifact{
		PrimaryKey: "k", SnapshotFiles: map[string][]byte{"k": []byte("D")}, BaselineVersionID: "v1"}}
	e := &ConfigPushExecutor{cos: cos, store: store,
		pushAll: func(LogFunc) ([]int, error) { return nil, fmt.Errorf("读取服务器列表失败") },
		gm:      func(LogFunc) {}}
	err := e.Execute(&model.WorkOrder{ID: 1}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "全服推送失败") {
		t.Fatalf("pushAll 出错应让工单失败, got %v", err)
	}
	if store.deployed != "" {
		t.Fatal("推送整体失败不应回填部署版本")
	}
}

func TestConfigPushBaselineNotFoundProceeds(t *testing.T) {
	cos := &stubCOS{headErr: coslib.ErrNotFound, putVer: "v2"}
	store := &stubArtifacts{art: ConfigArtifact{
		PrimaryKey:        "k",
		SnapshotFiles:     map[string][]byte{"k": []byte("D")},
		BaselineVersionID: "", // 线上无文件,基准为空 → 应继续
	}}
	e := &ConfigPushExecutor{cos: cos, store: store,
		pushAll: func(LogFunc) ([]int, error) { return nil, nil }, gm: func(LogFunc) {}}
	if err := e.Execute(&model.WorkOrder{ID: 1}, func(string) {}); err != nil {
		t.Fatalf("线上无文件+空基准应成功, got %v", err)
	}
	if string(cos.uploaded["k"]) != "D" {
		t.Fatal("应已上传快照")
	}
}
