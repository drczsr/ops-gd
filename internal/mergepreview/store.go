package mergepreview

import (
	"strconv"

	"gorm.io/gorm"
)

// 固定常量(新增行时自动套用)。只有这 3 列是固定值(源文件标黄):
// PreviewOpenTime(开服第几天显示预告)、IsGrandGift(是否有合服大礼)、MiaoShu(公告内容)。
// 其余列(预告持续几天 / 补偿入口开服天 / 补偿入口持续几天 / 是否有合服补偿)都需逐行填。
const (
	defPreviewOpenTime = 10
	defIsGrandGift     = 1
	defMiaoShu         = 945018
)

// Row 合服预告一行(固定 9 业务列 + 内部 seq)。
// seq 记录录入先后,仅用于列表「最新在前」排序;不属于业务列、不写进生成文件。
type Row struct {
	Seq                      int    `gorm:"column:seq;index"`
	ID                       int    `gorm:"column:Id;primaryKey"`
	Name                     string `gorm:"column:Name"`
	PreviewOpenTime          int    `gorm:"column:PreviewOpenTime"`
	PreviewDuration          int    `gorm:"column:PreviewDuration"`
	CompensationOpenTime     int    `gorm:"column:CompensationOpenTime"`
	CompensationDurationTime int    `gorm:"column:CompensationDurationTime"`
	IsCompensationItem       int    `gorm:"column:IsCompensationItem"`
	IsGrandGift              int    `gorm:"column:IsGrandGift"`
	MiaoShu                  int    `gorm:"column:MiaoShu"`
}

func (Row) TableName() string { return "merge_func_rows" }

// Meta 导入时捕获的原始表头 4 行(生成时原样还原)。
type Meta struct {
	ID        uint   `gorm:"column:id;primaryKey"`
	NamesLine string `gorm:"column:names_line"`
	TypesLine string `gorm:"column:types_line"`
	Row3Line  string `gorm:"column:row3_line"`
	Row4Line  string `gorm:"column:row4_line"`
}

func (Meta) TableName() string { return "merge_func_meta" }

// fields 把一行映射成 列名->字符串值(供按表头列序生成)。
func (r Row) fields() map[string]string {
	return map[string]string{
		"Id":                       strconv.Itoa(r.ID),
		"Name":                     r.Name,
		"PreviewOpenTime":          strconv.Itoa(r.PreviewOpenTime),
		"PreviewDuration":          strconv.Itoa(r.PreviewDuration),
		"CompensationOpenTime":     strconv.Itoa(r.CompensationOpenTime),
		"CompensationDurationTime": strconv.Itoa(r.CompensationDurationTime),
		"IsCompensationItem":       strconv.Itoa(r.IsCompensationItem),
		"IsGrandGift":              strconv.Itoa(r.IsGrandGift),
		"MiaoShu":                  strconv.Itoa(r.MiaoShu),
	}
}

// NewRow 用会变的列 + 3 个固定常量拼一行。
// previewDuration=预告持续几天, compOpenTime=开服第几天显示补偿入口,
// compDuration=补偿入口持续几天, isCompItem=是否有合服补偿。
func NewRow(id int, name string, previewDuration, compOpenTime, compDuration, isCompItem int) Row {
	return Row{
		ID:                       id,
		Name:                     name,
		PreviewOpenTime:          defPreviewOpenTime,
		PreviewDuration:          previewDuration,
		CompensationOpenTime:     compOpenTime,
		CompensationDurationTime: compDuration,
		IsCompensationItem:       isCompItem,
		IsGrandGift:              defIsGrandGift,
		MiaoShu:                  defMiaoShu,
	}
}

// Store 合服预告库(独立 MySQL;测试用 SQLite)。
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) EnsureSchema() error {
	if err := s.db.AutoMigrate(&Row{}, &Meta{}); err != nil {
		return err
	}
	// 历史行(seq=0,加列前已存在)按 Id 回填,给个稳定次序;
	// 之后追加的行 seq 继续从当前最大值递增,始终置顶。
	return s.db.Exec("UPDATE merge_func_rows SET seq = Id WHERE seq = 0").Error
}

// ReplaceAll 事务清空两表 + 写表头(ID=1) + 批量插入行(一次性导入/覆盖)。
func (s *Store) ReplaceAll(meta Meta, rows []Row) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM merge_func_rows").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM merge_func_meta").Error; err != nil {
			return err
		}
		meta.ID = 1
		if err := tx.Create(&meta).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			for i := range rows {
				rows[i].Seq = i + 1 // 按导入顺序(文件内 Id 升序)赋序号
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Append 事务内逐条插入;主键已存在的 Id 收集到 conflictIDs 并跳过(不覆盖)。
// 整批要么全提交(冲突跳过、其余入库),要么出错全回滚——不留半批。
func (s *Store) Append(rows []Row) ([]int, error) {
	var conflicts []int
	err := s.db.Transaction(func(tx *gorm.DB) error {
		conflicts = conflicts[:0]
		var maxSeq int
		if err := tx.Model(&Row{}).Select("COALESCE(MAX(seq),0)").Scan(&maxSeq).Error; err != nil {
			return err
		}
		for i := range rows {
			r := rows[i]
			var cnt int64
			if err := tx.Model(&Row{}).Where("Id = ?", r.ID).Count(&cnt).Error; err != nil {
				return err
			}
			if cnt > 0 {
				conflicts = append(conflicts, r.ID)
				continue
			}
			maxSeq++
			r.Seq = maxSeq // 新追加的行序号最大,列表里置顶
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return conflicts, nil
}

// List 按 Id/Name 模糊搜,seq 降序(最新录入在前),分页。
// 仅供页面展示;生成全量文件请用 AllRows(Id 升序)。
func (s *Store) List(query string, limit, offset int) ([]Row, error) {
	q := s.db.Model(&Row{})
	if query != "" {
		like := "%" + query + "%"
		cond := s.db.Where("Name LIKE ?", like)
		if id, err := strconv.Atoi(query); err == nil {
			cond = cond.Or("Id = ?", id)
		}
		q = q.Where(cond)
	}
	var rows []Row
	if err := q.Order("seq desc").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) Count(query string) (int64, error) {
	q := s.db.Model(&Row{})
	if query != "" {
		like := "%" + query + "%"
		cond := s.db.Where("Name LIKE ?", like)
		if id, err := strconv.Atoi(query); err == nil {
			cond = cond.Or("Id = ?", id)
		}
		q = q.Where(cond)
	}
	var n int64
	err := q.Count(&n).Error
	return n, err
}

func (s *Store) Delete(id int) error {
	return s.db.Where("Id = ?", id).Delete(&Row{}).Error
}

// AllRows 全量,按 seq 升序(=源文件原始行序,新追加的接末尾)。供生成文件用。
func (s *Store) AllRows() ([]Row, error) {
	var rows []Row
	if err := s.db.Order("seq asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) GetMeta() (Meta, error) {
	var m Meta
	err := s.db.First(&m, 1).Error
	return m, err
}
