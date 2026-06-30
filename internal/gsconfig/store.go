package gsconfig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const serverTable = "config_server"

type columnModel struct {
	Ordinal  int    `gorm:"primaryKey"`
	Name     string `gorm:"uniqueIndex;size:128"`
	GameType string `gorm:"size:32"`
	Row3Tag  string `gorm:"size:128"`
}

func (columnModel) TableName() string { return "config_columns" }

type metaModel struct {
	ID                  uint `gorm:"primaryKey"`
	HeaderCol1Directive string
	MaxID               int
	MaxRecord           int
	Row4Line            string
	Encoding            string `gorm:"size:16"`
}

func (metaModel) TableName() string { return "config_metas" }

// Store 配置库读写。驱动无关:生产 MySQL、测试 SQLite。
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// DB 返回底层 *gorm.DB(仅测试/装配用)。
func (s *Store) DB() *gorm.DB { return s.db }

// EnsureSchema 建固定表 + 按列动态建 config_server(所有列 VARCHAR(255),Id 为主键)。
func (s *Store) EnsureSchema(cols []Column) error {
	if err := s.db.AutoMigrate(&columnModel{}, &metaModel{}); err != nil {
		return err
	}
	ordered := orderedCols(cols)
	var defs []string
	for _, c := range ordered {
		if c.Name == "Id" {
			defs = append(defs, "`Id` VARCHAR(255) PRIMARY KEY")
		} else {
			defs = append(defs, fmt.Sprintf("`%s` VARCHAR(255)", c.Name))
		}
	}
	ddl := fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (%s)", serverTable, strings.Join(defs, ", "))
	return s.db.Exec(ddl).Error
}

// ImportAll 在事务里写三张表。
func (s *Store) ImportAll(cols []Column, meta Meta, rows []ServerRow) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, c := range cols {
			m := columnModel{Ordinal: c.Ordinal, Name: c.Name, GameType: c.GameType, Row3Tag: c.Row3Tag}
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
		}
		mm := metaModel{ID: 1, HeaderCol1Directive: meta.HeaderCol1Directive, MaxID: meta.MaxID,
			MaxRecord: meta.MaxRecord, Row4Line: meta.Row4Line, Encoding: meta.Encoding}
		if err := tx.Create(&mm).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if err := tx.Table(serverTable).Create(toAnyMap(r)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Columns() ([]Column, error) {
	var ms []columnModel
	if err := s.db.Order("ordinal asc").Find(&ms).Error; err != nil {
		return nil, err
	}
	cols := make([]Column, len(ms))
	for i, m := range ms {
		cols[i] = Column{Ordinal: m.Ordinal, Name: m.Name, GameType: m.GameType, Row3Tag: m.Row3Tag}
	}
	return cols, nil
}

func (s *Store) Meta() (Meta, error) {
	var m metaModel
	if err := s.db.First(&m, 1).Error; err != nil {
		return Meta{}, err
	}
	return Meta{HeaderCol1Directive: m.HeaderCol1Directive, MaxID: m.MaxID, MaxRecord: m.MaxRecord,
		Row4Line: m.Row4Line, Encoding: m.Encoding}, nil
}

// ColumnDescriptions 返回 列名 -> 中文描述。
// 描述来自文件第 4 行(Meta.Row4Line),按 tab 切开,与列按序号对齐;
// 第 1 个单元格形如 "#ID",去掉前导 '#'。某列无描述则不在 map 里。
func (s *Store) ColumnDescriptions() (map[string]string, error) {
	cols, err := s.Columns()
	if err != nil {
		return nil, err
	}
	meta, err := s.Meta()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(meta.Row4Line, "\t")
	out := make(map[string]string, len(cols))
	for i, c := range cols {
		if i >= len(parts) {
			break
		}
		d := strings.TrimSpace(strings.TrimPrefix(parts[i], "#"))
		if d != "" {
			out[c.Name] = d
		}
	}
	return out, nil
}

func (s *Store) GetServer(id string) (ServerRow, error) {
	var res map[string]any
	if err := s.db.Table(serverTable).Where("`Id` = ?", id).Take(&res).Error; err != nil {
		return nil, err
	}
	return toStrMap(res), nil
}

func (s *Store) UpdateServer(id string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return s.db.Table(serverTable).Where("`Id` = ?", id).Updates(toAnyMap(fields)).Error
}

// UpdateServers 事务批量更新:perID = 服号 -> {列名:新值}。整批全提交或全回滚。
func (s *Store) UpdateServers(perID map[string]map[string]string) error {
	if len(perID) == 0 {
		return nil
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for id, fields := range perID {
			if len(fields) == 0 {
				continue
			}
			if err := tx.Table(serverTable).Where("`Id` = ?", id).Updates(toAnyMap(fields)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) AddServer(id string, fields map[string]string) error {
	m := toAnyMap(fields)
	m["Id"] = id
	return s.db.Table(serverTable).Create(m).Error
}

// DeleteServer 从受管表删除指定 Id 的行;不存在视为已删(幂等)。
func (s *Store) DeleteServer(id string) error {
	return s.db.Exec("DELETE FROM "+serverTable+" WHERE `Id` = ?", id).Error
}

// AddServers 在一个事务里批量插入多行(每行的 map 须含 Id)。任一行失败整批回滚。
// 受管 schema 是动态的(列由导入文件决定),不同环境列集可能不同;故写入前按
// 配置库实际列过滤掉未知键,避免插入因 "Unknown column" 失败。
func (s *Store) AddServers(rows []map[string]string) error {
	known, err := s.knownColumns()
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, r := range rows {
			if err := tx.Table(serverTable).Create(filterCols(r, known)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// knownColumns 返回 config_server 的列名集合(来自 config_columns)。
func (s *Store) knownColumns() (map[string]bool, error) {
	cols, err := s.Columns()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(cols))
	for _, c := range cols {
		set[c.Name] = true
	}
	return set, nil
}

// filterCols 只保留 known 中的列,转成 gorm 可写的 map[string]any。
func filterCols(fields map[string]string, known map[string]bool) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		if known[k] {
			out[k] = v
		}
	}
	return out
}

// AllRows 全部服,按 Id 数值升序(在 Go 里排,避免方言差异)。表不存在时返回空。
func (s *Store) AllRows() ([]ServerRow, error) {
	if !s.db.Migrator().HasTable(serverTable) {
		return nil, nil
	}
	var raw []map[string]any
	if err := s.db.Table(serverTable).Find(&raw).Error; err != nil {
		return nil, err
	}
	rows := make([]ServerRow, len(raw))
	for i, m := range raw {
		rows[i] = toStrMap(m)
	}
	sort.Slice(rows, func(i, j int) bool { return idInt(rows[i]) < idInt(rows[j]) })
	return rows, nil
}

// ListServers 列表分页 + 按 Id/WorldName 模糊搜索;按 Id 数值序(LENGTH 技巧,两方言通用)。
// showAll=false 时只保留 Id>10000 且 WorldType∈{0,1,2,3} 的服(隐藏合服废弃/中心服/内部测试服);
// showAll=true 时不过滤,显示全部。`列名 + 0` 把文本数值化(MySQL/SQLite 通用)。
func (s *Store) ListServers(query string, showAll bool, limit, offset int) ([]ServerRow, error) {
	q := s.db.Table(serverTable)
	if !showAll {
		q = q.Where("`Id` + 0 > 10000").Where("`WorldType` + 0 BETWEEN 0 AND 3")
	}
	query = strings.TrimSpace(query)
	if query != "" {
		like := "%" + query + "%"
		conds := []string{"`Id` LIKE ?"}
		args := []any{like}
		if s.db.Migrator().HasColumn(serverTable, "Desc") {
			conds = append(conds, "`Desc` LIKE ?")
			args = append(args, like)
		}
		if s.db.Migrator().HasColumn(serverTable, "WorldName") {
			conds = append(conds, "`WorldName` LIKE ?")
			args = append(args, like)
		}
		q = q.Where(strings.Join(conds, " OR "), args...)
	}
	var raw []map[string]any
	if err := q.Order("LENGTH(`Id`)").Order("`Id`").Limit(limit).Offset(offset).Find(&raw).Error; err != nil {
		return nil, err
	}
	rows := make([]ServerRow, len(raw))
	for i, m := range raw {
		rows[i] = toStrMap(m)
	}
	return rows, nil
}

// Truncate 清空三张表(重导用)。
func (s *Store) Truncate() error {
	if err := s.db.Exec("DELETE FROM `" + serverTable + "`").Error; err != nil {
		return err
	}
	if err := s.db.Exec("DELETE FROM config_columns").Error; err != nil {
		return err
	}
	return s.db.Exec("DELETE FROM config_metas").Error
}

// DropManaged 删除三张受管表(强制重导用)。与 Truncate 不同,它连表结构一起删,
// 这样随后的 EnsureSchema 会按新文件的列重建 config_server——文件里有新增列时也能建出来
//(Truncate 只删数据、保留旧结构,新增列加不上)。
func (s *Store) DropManaged() error {
	for _, tbl := range []string{serverTable, "config_columns", "config_metas"} {
		if err := s.db.Exec("DROP TABLE IF EXISTS `" + tbl + "`").Error; err != nil {
			return err
		}
	}
	return nil
}

// regionColumnName 工单系统自维护的大区列;Row3Tag 非 server/client,生成文件时自动排除。
const regionColumnName = "WoRegion"
const regionRow3Tag = "internal"

// EnsureRegionColumn 幂等保证配置库存在 WoRegion 列(工单系统自维护、不进生成文件)。
// 已存在则直接返回。新建时追加 columnModel(Ordinal 取当前最大+1)+ ALTER TABLE 加列。
// 不回填:读取层对空值可按需回退 GroupName(ServerShowName),平滑过渡。
func (s *Store) EnsureRegionColumn() error {
	cols, err := s.Columns()
	if err != nil {
		return err
	}
	maxOrd := 0
	for _, c := range cols {
		if c.Name == regionColumnName {
			return nil // 已存在,幂等返回
		}
		if c.Ordinal > maxOrd {
			maxOrd = c.Ordinal
		}
	}
	m := columnModel{Ordinal: maxOrd + 1, Name: regionColumnName, GameType: "STRING", Row3Tag: regionRow3Tag}
	if err := s.db.Create(&m).Error; err != nil {
		return err
	}
	if s.db.Migrator().HasTable(serverTable) {
		return s.db.Exec(fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` VARCHAR(255)", serverTable, regionColumnName)).Error
	}
	return nil
}

func orderedCols(cols []Column) []Column {
	out := make([]Column, len(cols))
	copy(out, cols)
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// toStrMap 把 gorm 读出的任意值统一成字符串。
func toStrMap(m map[string]any) ServerRow {
	out := make(ServerRow, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case nil:
			out[k] = ""
		case []byte:
			out[k] = string(t)
		case string:
			out[k] = t
		case int64:
			out[k] = strconv.FormatInt(t, 10)
		default:
			out[k] = fmt.Sprintf("%v", t)
		}
	}
	return out
}
