package gsconfig

import (
	"fmt"
	"os"
)

// Service 编排:从库投影生成 server/client 两个部署文件 → 各传各的 COS 键(可选另存本地调试副本)。
type Service struct {
	store           *Store
	uploader        Uploader
	serverObjectKey string
	clientObjectKey string
	serverDebugPath string // 可空
	clientDebugPath string // 可空
}

func NewService(store *Store, up Uploader, serverKey, clientKey, serverDebug, clientDebug string) *Service {
	return &Service{
		store:           store,
		uploader:        up,
		serverObjectKey: serverKey,
		clientObjectKey: clientKey,
		serverDebugPath: serverDebug,
		clientDebugPath: clientDebug,
	}
}

func (s *Service) Store() *Store { return s.store }

// ServerSource 返回基于本服务 store 的 gameserver.Source 实现。
func (s *Service) ServerSource() *ServerSource { return NewServerSource(s.store) }

// GenerateAndPublish 投影生成 server/client 两份(UTF-8)并各传各的 COS 键。
// 两份都先在内存生成成功后再上传;任一步失败即返回错误(不上传半成品)。
func (s *Service) GenerateAndPublish() error {
	cols, err := s.store.Columns()
	if err != nil {
		return fmt.Errorf("读列定义: %w", err)
	}
	meta, err := s.store.Meta()
	if err != nil {
		return fmt.Errorf("读元信息: %w", err)
	}
	rows, err := s.store.AllRows()
	if err != nil {
		return fmt.Errorf("读数据行: %w", err)
	}

	serverData, err := GenerateFor(cols, meta, rows, "server")
	if err != nil {
		return fmt.Errorf("生成 server 文件: %w", err)
	}
	clientData, err := GenerateFor(cols, meta, rows, "client")
	if err != nil {
		return fmt.Errorf("生成 client 文件: %w", err)
	}

	if s.serverDebugPath != "" {
		if werr := os.WriteFile(s.serverDebugPath, serverData, 0644); werr != nil {
			return fmt.Errorf("写 server 本地副本失败: %w", werr)
		}
	}
	if s.clientDebugPath != "" {
		if werr := os.WriteFile(s.clientDebugPath, clientData, 0644); werr != nil {
			return fmt.Errorf("写 client 本地副本失败: %w", werr)
		}
	}

	if _, err := s.uploader.Upload(s.serverObjectKey, serverData); err != nil {
		return fmt.Errorf("上传 server 到 COS 失败: %w", err)
	}
	if _, err := s.uploader.Upload(s.clientObjectKey, clientData); err != nil {
		return fmt.Errorf("上传 client 到 COS 失败(server 已上传): %w", err)
	}
	return nil
}

// GenerateServerFile 从配置库生成 server 投影写到本地 path(供 merge.sh 读 IP/库凭据)。
func (s *Service) GenerateServerFile(path string) error {
	cols, err := s.store.Columns()
	if err != nil {
		return fmt.Errorf("读列定义: %w", err)
	}
	meta, err := s.store.Meta()
	if err != nil {
		return fmt.Errorf("读元信息: %w", err)
	}
	rows, err := s.store.AllRows()
	if err != nil {
		return fmt.Errorf("读数据行: %w", err)
	}
	data, err := GenerateFor(cols, meta, rows, "server")
	if err != nil {
		return fmt.Errorf("生成 server 文件: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// GenerateFullBytes 生成「全列」配置文件字节(供 Web 导出):列集与导入的原始 ServerConfigList.txt
// 一致,可被本系统的导入(/gsconfig/import 或 CLI -import-gsconfig)无损再导入。
func (s *Service) GenerateFullBytes() ([]byte, error) {
	cols, err := s.store.Columns()
	if err != nil {
		return nil, fmt.Errorf("读列定义: %w", err)
	}
	meta, err := s.store.Meta()
	if err != nil {
		return nil, fmt.Errorf("读元信息: %w", err)
	}
	rows, err := s.store.AllRows()
	if err != nil {
		return nil, fmt.Errorf("读数据行: %w", err)
	}
	return GenerateFor(cols, meta, rows, "all")
}

// GenerateFullGBKBytes 生成全列配置文件并编码为 GBK(用于 Web 导出兼容本地默认打开方式)。
func (s *Service) GenerateFullGBKBytes() ([]byte, error) {
	data, err := s.GenerateFullBytes()
	if err != nil {
		return nil, err
	}
	gbk, err := encodeGBK(data)
	if err != nil {
		return nil, fmt.Errorf("GBK encode: %w", err)
	}
	return gbk, nil
}
