package settings

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewStoreUsesDefaultsWhenNoPath(t *testing.T) {
	st, err := NewStore(Settings{Mode: "mock", AutoExecute: true}, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if st.Mode() != "mock" || !st.AutoExecute() {
		t.Errorf("got %+v", st.Get())
	}
}

func TestNewStoreMissingFileUsesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	st, err := NewStore(Settings{Mode: "real"}, p)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if st.Mode() != "real" {
		t.Errorf("mode = %q, want real", st.Mode())
	}
}

func TestUpdatePersistsAndReloads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	st, _ := NewStore(Settings{Mode: "mock"}, p)
	if err := st.Update(Settings{Mode: "real", AutoOpen: true, ShowAllServers: true}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if st.Mode() != "real" || !st.AutoOpen() || !st.ShowAllServers() {
		t.Errorf("内存未更新: %+v", st.Get())
	}
	st2, _ := NewStore(Settings{Mode: "mock"}, p)
	if st2.Mode() != "real" || !st2.AutoOpen() || !st2.ShowAllServers() {
		t.Errorf("文件未恢复: %+v", st2.Get())
	}
}

func TestNewStoreCorruptFileFallsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte("{not json"), 0644)
	st, err := NewStore(Settings{Mode: "mock"}, p)
	if err == nil {
		t.Error("损坏文件应返回错误供调用方记日志")
	}
	if st == nil || st.Mode() != "mock" {
		t.Error("损坏文件应回退默认值且不为 nil")
	}
}

func TestConcurrentAccessNoRace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	st, _ := NewStore(Settings{Mode: "mock"}, p)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = st.Get() }()
		go func() { defer wg.Done(); _ = st.Update(Settings{Mode: "real"}) }()
	}
	wg.Wait()
}
