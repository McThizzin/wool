package flags

import (
	"bytes"
	"testing"
)

const validCatalogJSON = `{
  "source": {"version": "b1", "commit": "c1"},
  "flags": [
    {
      "name": "--sample",
      "aliases": ["s", "s", "--sample"],
      "value": "int",
      "description": "A sample flag.",
      "category": "general"
    }
  ]
}`

func TestEmbeddedCatalogIsCompleteAndCopied(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Flags) < 20 {
		t.Fatalf("embedded catalog has only %d flags", len(c.Flags))
	}
	if c.Source.Version != "b6000" || c.Source.Commit != "a1b2c3d" {
		t.Fatalf("source = %+v", c.Source)
	}
	for i, f := range c.Flags {
		if f.Name == "" || f.Name != Normalize(f.Name) {
			t.Fatalf("flag %d has invalid name %q", i, f.Name)
		}
		if i > 0 && c.Flags[i-1].Name >= f.Name {
			t.Fatalf("flags are not sorted at %d: %q >= %q", i, c.Flags[i-1].Name, f.Name)
		}
		if f.Aliases == nil {
			t.Fatalf("flag %q has nil aliases", f.Name)
		}
		if f.Value == "" || f.Description == "" || f.Category == "" {
			t.Fatalf("flag %q is missing metadata: %+v", f.Name, f)
		}
		if f.Source != c.Source {
			t.Fatalf("flag %q source = %+v, want %+v", f.Name, f.Source, c.Source)
		}
		if f.NeedsValue() == (f.Value == ValueNone) {
			t.Fatalf("flag %q value shape %q is inconsistent", f.Name, f.Value)
		}
	}

	c.Flags[0].Name = "mutated"
	c.Flags[0].Aliases = append(c.Flags[0].Aliases, "mutated")
	again, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if again.Flags[0].Name == "mutated" || len(again.Flags[0].Aliases) == len(c.Flags[0].Aliases) {
		t.Fatal("Embedded returned a shared value")
	}
}

func TestDecodeNormalizesAndValidates(t *testing.T) {
	c, err := Decode([]byte(validCatalogJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Flags) != 1 {
		t.Fatalf("flags = %d", len(c.Flags))
	}
	f := c.Flags[0]
	if f.Name != "sample" {
		t.Fatalf("name = %q", f.Name)
	}
	if len(f.Aliases) != 1 || f.Aliases[0] != "s" {
		t.Fatalf("aliases = %q", f.Aliases)
	}
	if f.Source != c.Source {
		t.Fatalf("source was not inherited: %+v", f.Source)
	}

	cases := []struct {
		name string
		json string
	}{
		{"missing version", `{"source":{"commit":"c"},"flags":[{"name":"a","value":"int","description":"d","category":"g"}]}`},
		{"empty catalog", `{"source":{"version":"b","commit":"c"},"flags":[]}`},
		{"no name", `{"source":{"version":"b","commit":"c"},"flags":[{"name":"--","value":"int","description":"d","category":"g"}]}`},
		{"duplicate name", `{"source":{"version":"b","commit":"c"},"flags":[{"name":"a","value":"int","description":"d","category":"g"},{"name":"--a","value":"int","description":"d","category":"g"}]}`},
		{"no value", `{"source":{"version":"b","commit":"c"},"flags":[{"name":"a","description":"d","category":"g"}]}`},
		{"no description", `{"source":{"version":"b","commit":"c"},"flags":[{"name":"a","value":"int","category":"g"}]}`},
		{"no category", `{"source":{"version":"b","commit":"c"},"flags":[{"name":"a","value":"int","description":"d"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(tc.json)); err == nil {
				t.Fatal("Decode accepted an invalid catalog")
			}
		})
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	c, err := Decode([]byte(`{
    "source": {"version": "b1", "commit": "c1"},
    "flags": [
      {"name": "zebra", "aliases": [], "value": "none", "description": "z", "category": "g"},
      {"name": "alpha", "aliases": ["a"], "value": "int", "description": "a", "category": "g"}
    ]
  }`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(first, []byte("\n")) {
		t.Fatal("Encode output has no trailing newline")
	}
	c.Flags[0], c.Flags[1] = c.Flags[1], c.Flags[0]
	second, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("Encode is not deterministic:\n%s\n---\n%s", first, second)
	}
}

func TestLookupAcceptsNameAndAlias(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{"ctx-size", "--ctx-size", "-c"} {
		f, ok := c.Lookup(spelling)
		if !ok || f.Name != "ctx-size" {
			t.Fatalf("Lookup(%q) = %q, %v", spelling, f.Name, ok)
		}
	}
	if _, ok := c.Lookup("not-a-llama-flag"); ok {
		t.Fatal("Lookup accepted an unknown flag")
	}
}
