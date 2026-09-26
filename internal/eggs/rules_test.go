package eggs

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateRules(t *testing.T) {
	cases := []struct {
		rules string
		val   string
		ok    bool
	}{
		{"required|string|max:20", "Paper", true},
		{"required|string|max:20", "", false},
		{"required|string|max:5", "abcdef", false},
		{"nullable|string|max:5", "", true},
		{"string|max:20", "", false}, // null without nullable fails string (Laravel)
		{"max:20", "", true},         // but size rules see length 0
		{"required|integer|between:1024,65535", "25565", true},
		{"required|integer|between:1024,65535", "80", false},
		{"required|integer", "08", false},
		{"required|integer", "-3", true},
		{"required|numeric|min:1", "0.5", false},
		{"required|numeric|min:1", "1.5", true},
		{"required|numeric", "1e3", true},
		{"required|string|min:4", "1", false},     // no numeric rule: length
		{"required|integer|max:8", "7778", false}, // numeric rule: value
		{"required|boolean", "1", true},
		{"required|boolean", "0", true},
		{"required|boolean", "true", false},
		{"required|string|in:true,false", "true", true},
		{"required|in:a,b", "c", false},
		{`required|in:"a,b",c`, "a,b", true},
		{"required|in: DefaultStart,Brutal", "DefaultStart", true},
		{"nullable|not_in:x", "x", false},
		{"required|digits_between:17,18", "123456789012345678", true},
		{"required|digits_between:17,18", "12345", false},
		{"required|digits:4", "1234", true},
		{"required|size:3", "abc", true},
		{"required|numeric|gt:0", "0", false},
		{"required|alpha_dash", "my-world_1", true},
		{"required|alpha_dash", "my world", false},
		{"required|alpha_num", "abc123", true},
		{"required|url", "https://example.com/x", true},
		{"required|url", "example.com", false},
		{"required|ends_with:.jar", "server.jar", true},
		{`required|regex:/^([\w\d._-]+)(\.jar)$/`, "server.jar", true},
		{`required|regex:/^([\w\d._-]+)(\.jar)$/`, "server.zip", false},
		{`required|regex:([a-z-0-9]+$)`, "abc-1", true}, // bracket delimiters
		{`required|regex:/^(TRUE)$/i`, "true", true},
		{`required|regex:/^(\w{1,20})$/`, "a|b", false}, // pipe kept inside the regex
		{"nullable|regex:/^a$/", "", true},
		{"required|unknown_rule:1", "x", true}, // unknown rules are skipped
	}
	for _, c := range cases {
		v := Variable{Env: "X", Rules: parseRules(scalarNode(c.rules))}
		errs := v.check(c.val)
		if (len(errs) == 0) != c.ok {
			t.Errorf("rules %q, value %q: ok=%v, want %v (%v)", c.rules, c.val, len(errs) == 0, c.ok, errs)
		}
	}
}

func TestValidateDefaultsAndErrors(t *testing.T) {
	e := &Egg{Variables: []Variable{
		{Name: "Version", Env: "VERSION", Default: "latest", Rules: []string{"required", "string", "max:20"}},
		{Name: "Port", Env: "QUERY_PORT", Default: "", Rules: []string{"required", "integer"}},
	}}
	out, err := e.Validate(map[string]string{"QUERY_PORT": "25565", "IGNORED": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if out["VERSION"] != "latest" || out["QUERY_PORT"] != "25565" || len(out) != 2 {
		t.Fatalf("out = %v", out)
	}
	_, err = e.Validate(nil)
	var ve VariableErrors
	if !errors.As(err, &ve) || len(ve) != 2 || ve[0].Env != "QUERY_PORT" || !strings.Contains(err.Error(), "is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestPHPRegex(t *testing.T) {
	for _, p := range []string{`/^a+$/`, `#^a+$#i`, `{^a+$}`, `/^a\/b$/`, `/a/u`, `/a/A`} {
		if _, err := phpRegex(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{`abc`, `/a(?=b)/`, `/a/x`, `/(a)\1/`, ``, `/abc`} {
		if _, err := phpRegex(p); err == nil {
			t.Errorf("%s: expected an error", p)
		}
	}
	re, _ := phpRegex(`/b/A`)
	if re.MatchString("ab") {
		t.Error("A modifier should anchor at the start")
	}
}

func TestLint(t *testing.T) {
	e := &Egg{Variables: []Variable{{Env: "X", Rules: []string{"required", "exists:users", `regex:/a(?=b)/`}}}}
	got := e.Lint()
	if len(got) != 2 {
		t.Fatalf("lint = %v", got)
	}
}

func scalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}
