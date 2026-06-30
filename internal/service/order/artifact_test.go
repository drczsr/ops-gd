package order

import (
	"testing"

	"gongdan/internal/model"
)

func TestCreateWithArtifactRoundTrip(t *testing.T) {
	gdb := newTestDB(t)
	s := New(gdb, mockStore(t), nil)
	files := map[string][]byte{"server/ServerConfigList.txt": {0x01, 0x02, 0xff}} // 含非UTF8字节
	wo, err := s.CreateWithArtifact(model.TypeConfigPush, "全服更新", "全服",
		"server/ServerConfigList.txt", files, "v1", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	art, err := s.GetArtifact(wo.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if art.PrimaryKey != "server/ServerConfigList.txt" || art.BaselineVersionID != "v1" {
		t.Fatalf("art = %+v", art)
	}
	got := art.SnapshotFiles["server/ServerConfigList.txt"]
	if len(got) != 3 || got[2] != 0xff {
		t.Fatalf("快照字节未原样保留: %v", got)
	}
	if err := s.SetDeployedVersion(wo.ID, "v2"); err != nil {
		t.Fatalf("setdeployed: %v", err)
	}
	var row model.WorkOrderArtifact
	s.db.Where("order_id = ?", wo.ID).First(&row)
	if row.DeployedVersionID != "v2" {
		t.Fatalf("deployed = %q", row.DeployedVersionID)
	}
}
