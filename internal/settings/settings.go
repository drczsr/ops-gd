// Package settings 提供运行时可在线修改的运营开关存储。
// config.yaml 作为默认基线;本包把这几个值持久化到独立的 settings.json,
// 不回写 config.yaml(保留其注释)。Store 是这些值的运行时单一数据源,线程安全。
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Settings 运营开关集合。
type Settings struct {
	Mode           string `json:"mode"`             // "mock" | "real"
	AutoExecute    bool   `json:"auto_execute"`     // 审批通过后自动执行
	AutoOpen       bool   `json:"auto_open"`        // 测试通过后自动开放
	ShowAllServers bool   `json:"show_all_servers"` // 选服列表显示全部
}

// Store 线程安全地持有 Settings,并持久化到 path(path 为空表示不落盘,用于测试)。
type Store struct {
	mu   sync.RWMutex
	cur  Settings
	path string
}

// NewStore 以 defaults 为基线;若 path 非空且文件存在,则用文件内容覆盖。
// 文件缺失:用默认值,err=nil。文件损坏:用默认值,返回 err 供调用方记日志(不阻塞启动)。
func NewStore(defaults Settings, path string) (*Store, error) {
	s := &Store{cur: defaults, path: path}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	loaded := defaults // 从默认值起,JSON 缺省字段保留默认
	if err := json.Unmarshal(data, &loaded); err != nil {
		return s, fmt.Errorf("settings.json 解析失败,已回退默认值: %w", err)
	}
	s.cur = loaded
	return s, nil
}

// Get 返回当前快照。
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Update 改内存并落盘(先写临时文件再 rename,原子)。落盘失败则内存不变。
func (s *Store) Update(n Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if err := persist(s.path, n); err != nil {
			return err
		}
	}
	s.cur = n
	return nil
}

func persist(path string, n Settings) error {
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// 细粒度 getter(执行热路径用,避免拷整个结构)。
func (s *Store) Mode() string         { s.mu.RLock(); defer s.mu.RUnlock(); return s.cur.Mode }
func (s *Store) AutoExecute() bool    { s.mu.RLock(); defer s.mu.RUnlock(); return s.cur.AutoExecute }
func (s *Store) AutoOpen() bool       { s.mu.RLock(); defer s.mu.RUnlock(); return s.cur.AutoOpen }
func (s *Store) ShowAllServers() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.cur.ShowAllServers }
