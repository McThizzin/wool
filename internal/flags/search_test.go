package flags

import "testing"

func searchFixture() *Catalog {
	return &Catalog{
		Source: CatalogSource{Version: "b1", Commit: "c1"},
		Flags: []Flag{
			{Name: "model", Value: ValuePath, Description: "model path", Category: "model"},
			{Name: "ctx-size", Value: ValueInt, Description: "prompt context size", Category: "context"},
			{Name: "verbose", Value: ValueNone, Description: "enable verbose logging", Category: "logging"},
			{Name: "context-shift", Value: ValueInt, Description: "unused context shift", Category: "context"},
		},
	}
}

func TestSearchRanksAndTiesAreDeterministic(t *testing.T) {
	c := searchFixture()
	want := "ctx-size"
	for shift := 0; shift < len(c.Flags); shift++ {
		got := Search(c, "ctx", 0)
		if len(got) == 0 || got[0].Flag.Name != want {
			t.Fatalf("shift %d: first result = %+v, want %s", shift, got, want)
		}
		c.Flags[0], c.Flags[shift] = c.Flags[shift], c.Flags[0]
	}

	all := Search(c, "", 0)
	if len(all) != len(c.Flags) {
		t.Fatalf("empty query returned %d of %d flags", len(all), len(c.Flags))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Flag.Name >= all[i].Flag.Name {
			t.Fatalf("empty query is not name-sorted: %+v", all)
		}
	}
}

func TestSearchMatchesAliases(t *testing.T) {
	c := &Catalog{Flags: []Flag{
		{Name: "ctx-size", Value: ValueInt, Description: "context size", Category: "context"},
		{Name: "context", Aliases: []string{"ctx"}, Value: ValueInt, Description: "context mode", Category: "context"},
	}}
	got := Search(c, "-ctx", 2)
	if len(got) != 2 || got[0].Flag.Name != "context" {
		t.Fatalf("results = %+v", got)
	}
}

func TestSearchMatchesDescriptionsAndLimits(t *testing.T) {
	c := &Catalog{Flags: []Flag{
		{Name: "alpha", Value: ValueNone, Description: "sets sampling temperature", Category: "general"},
		{Name: "beta", Value: ValueNone, Description: "unrelated", Category: "general"},
		{Name: "zeta", Value: ValueNone, Description: "sets sampling temperature", Category: "general"},
	}}
	got := Search(c, "temperature", 2)
	if len(got) != 2 {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Flag.Name != "alpha" || got[1].Flag.Name != "zeta" {
		t.Fatalf("description matches are not deterministic: %+v", got)
	}
	if len(Search(c, "no-such-flag", 0)) != 0 {
		t.Fatal("unrelated query returned results")
	}
}
