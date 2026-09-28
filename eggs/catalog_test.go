package catalog

import (
	"slices"
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
)

func TestCatalog(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 18 {
		t.Fatalf("only %d entries", len(all))
	}
	for _, e := range all {
		egg, err := eggs.Parse(e.Egg)
		if err != nil {
			t.Fatal(err)
		}
		// The image a test picks must be one the egg offers; Wings refuses
		// any other.
		if img := e.Test.Image; img != "" && !slices.ContainsFunc(egg.Images, func(i eggs.Image) bool { return i.Ref == img }) {
			t.Errorf("%s: test image %s isn't one of the egg's images", e.ID, img)
		}
		// Test variables must be the egg's (a typo would silently do nothing)
		// and pass its own rules, as in the Panel.
		for k := range e.Test.Variables {
			if !slices.ContainsFunc(egg.Variables, func(v eggs.Variable) bool { return v.Env == k }) {
				t.Errorf("%s: test variable %s isn't one of the egg's", e.ID, k)
			}
		}
		if _, err := egg.Validate(e.Test.Variables); err != nil && e.Test.Tier != TierManual {
			t.Errorf("%s: variables: %v", e.ID, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	good := all[0]
	for name, mutate := range map[string]func(*Entry){
		"short commit":   func(e *Entry) { e.Source.Commit = "abc123" },
		"wrong file":     func(e *Entry) { e.Source.Path = "x/other.json" },
		"no license":     func(e *Entry) { e.License = "" },
		"bad arch":       func(e *Entry) { e.Arch = []string{"x86_64"} },
		"bad tier":       func(e *Entry) { e.Test.Tier = "sometimes" },
		"manual, no why": func(e *Entry) { e.Test.Tier, e.Test.Reason = TierManual, "" },
		"command alone":  func(e *Entry) { e.Test.Command, e.Test.Expect = "list", "" },
		"broken egg":     func(e *Entry) { e.Egg = []byte("{") },
		"bad regexp":     func(e *Entry) { e.Test.Command, e.Test.Expect = "list", "(" },
		"ready alone":    func(e *Entry) { e.Test.Command, e.Test.Expect, e.Test.Ready = "", "", "x" },
	} {
		e := good
		e.Arch = slices.Clone(good.Arch)
		mutate(&e)
		if e.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
