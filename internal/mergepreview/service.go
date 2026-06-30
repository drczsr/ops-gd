package mergepreview

import "fmt"

// Service 合服预告库读写 + 全量文件生成。推送已迁移到工单执行器,这里不再持有 COS/pusher。
type Service struct {
	store *Store
}

func NewService(store *Store) *Service { return &Service{store: store} }

func (s *Service) Store() *Store { return s.store }

// ImportOverwrite 一次性导入:校验→解析→清空重灌。
func (s *Service) ImportOverwrite(text string) error {
	if err := Validate(text); err != nil {
		return err
	}
	meta, rows, err := ParseImport(text)
	if err != nil {
		return err
	}
	return s.store.ReplaceAll(meta, rows)
}

// AppendRows 追加新行;返回主键冲突未追加的 Id。
func (s *Service) AppendRows(rows []Row) ([]int, error) { return s.store.Append(rows) }

func (s *Service) List(query string, limit, offset int) ([]Row, error) {
	return s.store.List(query, limit, offset)
}
func (s *Service) Count(query string) (int64, error) { return s.store.Count(query) }
func (s *Service) Delete(id int) error               { return s.store.Delete(id) }

// GenerateBytes 从库生成全量 MergeServerFunction.txt 字节(空库报错)。建单时用。
func (s *Service) GenerateBytes() ([]byte, error) {
	meta, err := s.store.GetMeta()
	if err != nil {
		return nil, fmt.Errorf("请先一次性导入全量(库里没有表头): %w", err)
	}
	rows, err := s.store.AllRows()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("库里没有数据行")
	}
	return GenerateFile(meta, rows)
}
