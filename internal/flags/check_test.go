package flags

import (
	"reflect"
	"testing"
)

func checkFixture() (*Catalog, *Catalog) {
	catalog := &Catalog{
		Source: CatalogSource{Version: "b1", Commit: "c1"},
		Flags: []Flag{
			{Name: "same", Aliases: []string{"s"}, Value: ValueInt, Description: "same", Default: "1", Category: "general"},
			{Name: "missing", Value: ValueNone, Description: "old flag", Category: "general"},
		},
	}
	installed := &Catalog{
		Source: CatalogSource{Version: "b2", Commit: "c2"},
		Flags: []Flag{
			{Name: "same", Aliases: []string{"s", "x"}, Value: ValueString, Description: "changed", Default: "1", Category: "general"},
			{Name: "new", Value: ValueNone, Description: "new flag", Category: "general"},
		},
	}
	return installed, catalog
}

func TestCompareReportsDrift(t *testing.T) {
	installed, catalog := checkFixture()
	got := Compare(installed, catalog)
	if got.OK {
		t.Fatal("Compare reported no drift")
	}
	if got.CatalogSource != catalog.Source || got.InstalledSource != installed.Source {
		t.Fatalf("sources = %+v", got)
	}
	if !reflect.DeepEqual(got.New, []string{"new"}) {
		t.Fatalf("new = %q", got.New)
	}
	if !reflect.DeepEqual(got.Missing, []string{"missing"}) {
		t.Fatalf("missing = %q", got.Missing)
	}
	wantChanged := []Change{{Name: "same", Fields: []string{"aliases", "description", "value"}}}
	if !reflect.DeepEqual(got.Changed, wantChanged) {
		t.Fatalf("changed = %+v, want %+v", got.Changed, wantChanged)
	}
}

func TestCompareOKForIdenticalCatalogs(t *testing.T) {
	installed, catalog := checkFixture()
	installed.Flags = append([]Flag(nil), catalog.Flags...)
	installed.Source = catalog.Source
	got := Compare(installed, catalog)
	if !got.OK || len(got.New) != 0 || len(got.Missing) != 0 || len(got.Changed) != 0 {
		t.Fatalf("result = %+v", got)
	}
	if got.New == nil || got.Missing == nil || got.Changed == nil {
		t.Fatal("empty slices must be encoded as []")
	}
}
