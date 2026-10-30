package theme

import (
	"strings"
	"testing"
)

func TestRegisterAllOverride(t *testing.T) {
	t.Cleanup(func() { _ = RegisterAll(nil) })
	defs := map[string]Definition{
		"ocean": {
			Base: "nord",
			Colors: map[string]string{
				"accent": "#00aaff",
				"border": "42",
			},
		},
	}
	if err := RegisterAll(defs); err != nil {
		t.Fatal(err)
	}
	th, ok := Get("ocean")
	if !ok {
		t.Fatal("custom theme missing")
	}
	if th.Name != "ocean" || th.Accent != "#00aaff" || th.Border != "42" {
		t.Fatalf("theme = %+v", th)
	}
	if th.Text != "#eceff4" {
		t.Fatalf("base text not inherited: %q", th.Text)
	}
	order := Order()
	if len(order) != len(builtinOrder)+1 || order[len(order)-1] != "ocean" {
		t.Fatalf("order = %v", order)
	}
	// Re-registering replaces the previous custom set.
	if err := RegisterAll(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get("ocean"); ok {
		t.Fatal("custom theme survived reset")
	}
	if got := Order(); len(got) != len(builtinOrder) {
		t.Fatalf("order after reset = %v", got)
	}
}

func TestRegisterAllDependencyOrder(t *testing.T) {
	t.Cleanup(func() { _ = RegisterAll(nil) })
	defs := map[string]Definition{
		"child":  {Base: "parent", Colors: map[string]string{"accent": "#010203"}},
		"parent": {Base: "tokyo", Colors: map[string]string{"text": "#040506"}},
	}
	if err := RegisterAll(defs); err != nil {
		t.Fatal(err)
	}
	child, ok := Get("child")
	if !ok {
		t.Fatal("child missing")
	}
	if child.Accent != "#010203" || child.Text != "#040506" {
		t.Fatalf("child = %+v", child)
	}
	if got := Order(); strings.Join(got, ",") != "mocha,tokyo,gruvbox,nord,child,parent" {
		t.Fatalf("order = %v", got)
	}
}

func TestRegisterAllErrors(t *testing.T) {
	t.Cleanup(func() { _ = RegisterAll(nil) })
	cases := []struct {
		name string
		defs map[string]Definition
		want string
	}{
		{"builtin", map[string]Definition{"mocha": {}}, "cannot override"},
		{"unknown base", map[string]Definition{"x": {Base: "nope"}}, "unknown theme"},
		{"cycle", map[string]Definition{
			"a": {Base: "b"},
			"b": {Base: "a"},
		}, "cycle"},
		{"bad color", map[string]Definition{"x": {Colors: map[string]string{"accent": "nope"}}}, "invalid color"},
		{"bad key", map[string]Definition{"x": {Colors: map[string]string{"shine": "#fff"}}}, "unknown color key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := RegisterAll(tc.defs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateAllDoesNotRegister(t *testing.T) {
	t.Cleanup(func() { _ = RegisterAll(nil) })
	if err := ValidateAll(map[string]Definition{"x": {Base: "mocha"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get("x"); ok {
		t.Fatal("ValidateAll registered a theme")
	}
}
