package executor

import (
	"errors"
	"fmt"

	coslib "gongdan/internal/gsconfig/cos"
)

// COSClient 配置快照所需的 COS 读写(由 gsconfig/cos.Uploader 实现)。
type COSClient interface {
	Upload(key string, data []byte) (versionID string, err error)
	Download(key string, versionID ...string) ([]byte, error)
	HeadVersionID(key string) (versionID string, err error)
}

// ConfigArtifact 一张配置类工单的快照工件(运行态,已 base64 解码)。
type ConfigArtifact struct {
	PrimaryKey        string            // 主文件键(基准校验/回填)
	SnapshotFiles     map[string][]byte // 待上传的全部文件:key->原始字节
	BaselineVersionID string            // 建单时线上主文件版本 id
}

// ArtifactStore 读取工件 + 回填部署版本 id(由 order.Service 实现)。
type ArtifactStore interface {
	GetArtifact(orderID uint) (ConfigArtifact, error)
	SetDeployedVersion(orderID uint, versionID string) error
}

// pushSnapshotAndReload 配置类工单执行的共用尾:
// ①基准校验(线上主文件版本 != 建单基准 → 拒绝)②逐文件上传冻结快照
// ③全服 gd_download 分波重试 ④GM热更 reload(尽力而为)⑤回填部署版本 id。
// release 复用:pushAll 提供 PushConfigAll 的失败服列表,gm 提供 gmHotUpdate。
func pushSnapshotAndReload(
	orderID uint, art ConfigArtifact, cos COSClient, store ArtifactStore,
	pushAll func(log LogFunc) ([]int, error), gm func(log LogFunc), log LogFunc,
) error {
	cur, err := cos.HeadVersionID(art.PrimaryKey)
	if err != nil && !errors.Is(err, coslib.ErrNotFound) {
		return fmt.Errorf("读线上当前版本: %w", err)
	}
	if cur != art.BaselineVersionID {
		return fmt.Errorf("基准已过期(线上版本 %q ≠ 建单基准 %q),请重新发起", cur, art.BaselineVersionID)
	}
	var deployedVer string
	for key, data := range art.SnapshotFiles {
		ver, uerr := cos.Upload(key, data)
		if uerr != nil {
			return fmt.Errorf("上传快照 %s: %w", key, uerr)
		}
		log(fmt.Sprintf("  [上传] %s 已写入 COS(版本 %s)", key, ver))
		if key == art.PrimaryKey {
			deployedVer = ver
		}
	}
	log("本次将刷新各服:ServerConfigList.txt + MergeServerFunction.txt(gd_download 同时拉取两份)")

	failed, perr := pushAll(log)
	if perr != nil {
		return fmt.Errorf("全服推送失败: %w", perr)
	}
	gm(log)

	if deployedVer != "" {
		if serr := store.SetDeployedVersion(orderID, deployedVer); serr != nil {
			log(fmt.Sprintf("  [告警] 回填部署版本失败(不影响下发): %v", serr))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("以下服更新失败: %v", failed)
	}
	return nil
}
