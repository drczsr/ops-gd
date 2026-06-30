// Package configdiff 提供两份配置表的单元格级差异计算(纯函数,无外部依赖)。
package configdiff

// Table 一份配置的表格视图:有序列 + 有序行 + 主键列名。
type Table struct {
	Cols   []string            // 有序列名(展示顺序)
	Rows   []map[string]string // 有序行(列名->值)
	KeyCol string              // 主键列名(如 "Id")
}

// CellChange 一处单元格改动。
type CellChange struct {
	Key string `json:"key"`
	Col string `json:"col"`
	Old string `json:"old"`
	New string `json:"new"`
}

// DiffResult 差异结果。
type DiffResult struct {
	Changed     []CellChange `json:"changed"`
	AddedKeys   []string     `json:"added"`
	RemovedKeys []string     `json:"removed"`
}

// Empty 无任何差异。
func (d DiffResult) Empty() bool {
	return len(d.Changed) == 0 && len(d.AddedKeys) == 0 && len(d.RemovedKeys) == 0
}

func index(t Table) map[string]map[string]string {
	m := make(map[string]map[string]string, len(t.Rows))
	for _, r := range t.Rows {
		m[r[t.KeyCol]] = r
	}
	return m
}

// Diff 比对 old→new:按主键匹配行,逐列比对值;new 有 old 无=新增,old 有 new 无=删除。
// 列以 new.Cols 为准(线上多出的列不参与改动判定)。改动/新增/删除均按 new(或 old)的行序输出。
func Diff(old, new Table) DiffResult {
	oldIdx, newIdx := index(old), index(new)
	var d DiffResult
	for _, r := range new.Rows {
		key := r[new.KeyCol]
		o, ok := oldIdx[key]
		if !ok {
			d.AddedKeys = append(d.AddedKeys, key)
			continue
		}
		for _, col := range new.Cols {
			if col == new.KeyCol {
				continue
			}
			if r[col] != o[col] {
				d.Changed = append(d.Changed, CellChange{Key: key, Col: col, Old: o[col], New: r[col]})
			}
		}
	}
	for _, r := range old.Rows {
		if _, ok := newIdx[r[old.KeyCol]]; !ok {
			d.RemovedKeys = append(d.RemovedKeys, r[old.KeyCol])
		}
	}
	return d
}
