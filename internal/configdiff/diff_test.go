package configdiff

import "testing"

func tbl(rows ...map[string]string) Table {
	return Table{Cols: []string{"Id", "Name", "V"}, KeyCol: "Id", Rows: rows}
}

func TestDiffChangedCell(t *testing.T) {
	old := tbl(map[string]string{"Id": "1", "Name": "a", "V": "10"})
	neu := tbl(map[string]string{"Id": "1", "Name": "a", "V": "11"})
	d := Diff(old, neu)
	if len(d.Changed) != 1 || d.Changed[0].Key != "1" || d.Changed[0].Col != "V" ||
		d.Changed[0].Old != "10" || d.Changed[0].New != "11" {
		t.Fatalf("changed = %+v", d.Changed)
	}
	if d.Empty() {
		t.Fatal("应非空")
	}
}

func TestDiffAddedRemoved(t *testing.T) {
	old := tbl(map[string]string{"Id": "1", "Name": "a", "V": "1"})
	neu := tbl(map[string]string{"Id": "2", "Name": "b", "V": "2"})
	d := Diff(old, neu)
	if len(d.AddedKeys) != 1 || d.AddedKeys[0] != "2" {
		t.Fatalf("added = %v", d.AddedKeys)
	}
	if len(d.RemovedKeys) != 1 || d.RemovedKeys[0] != "1" {
		t.Fatalf("removed = %v", d.RemovedKeys)
	}
}

func TestDiffEmptyBaselineAllNew(t *testing.T) {
	d := Diff(Table{KeyCol: "Id"}, tbl(map[string]string{"Id": "1", "Name": "a", "V": "1"}))
	if len(d.AddedKeys) != 1 {
		t.Fatalf("added = %v", d.AddedKeys)
	}
	if d.Empty() {
		t.Fatal("空基准+有行应非空")
	}
}

func TestDiffNoChange(t *testing.T) {
	r := map[string]string{"Id": "1", "Name": "a", "V": "1"}
	if !Diff(tbl(r), tbl(map[string]string{"Id": "1", "Name": "a", "V": "1"})).Empty() {
		t.Fatal("无变化应 Empty")
	}
}
