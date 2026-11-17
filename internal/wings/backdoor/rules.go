// Package backdoor finds known backdoor patterns in a game server's
// scripts (#428): FiveM resources bought or leaked with hidden code that
// downloads and runs a stranger's code, or hands them the console. It only
// reads and reports; it never changes or deletes a file. The rules are
// here, in one file, and ship (and update) with Wings.
//
// The rules aim at what's rare in honest resources: a hit means "look at
// this line", and its severity says how rarely honest code does it.
package backdoor

import (
	"bytes"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Severity is how sure a finding is.
type Severity string

const (
	// High: honest resources almost never do this (code fetched from the
	// internet and run, a known backdoor panel, a shell command).
	High Severity = "high"
	// Medium: unusual, worth a look (obfuscated code, granting admin).
	Medium Severity = "medium"
	// Low: also common in honest code (running code from a string).
	Low Severity = "low"
)

// Finding is one suspicious line.
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line"` // from 1
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	// Title says what the line does, in a sentence.
	Title string `json:"title"`
	// Excerpt is the part of the line that matched (decoded, when the
	// line hid it in escapes), at most excerptLen bytes.
	Excerpt string `json:"excerpt"`
}

// RuleSets are the rule sets an egg can name in x-raptor.scan.
var RuleSets = []string{"fivem"}

// Extensions are the files a rule set reads.
func Extensions(set string) []string {
	if set == "fivem" {
		return []string{".lua", ".js", ".cjs", ".mjs"}
	}
	return nil
}

type lang int

const (
	anyLang lang = iota
	luaLang
	jsLang
)

type rule struct {
	id    string
	sev   Severity
	title string
	lang  lang
	// re is matched against the line with its escapes decoded; with raw,
	// against the line as written.
	re *regexp.Regexp
	// any are words one of which the line must hold (lowercased) before
	// re runs: most lines hold none, and re is far slower.
	any []string
	raw bool
	// hidden: the match counts only when the line as written doesn't
	// show it, so it was hidden in escapes or character codes.
	hidden bool
}

// The rules, most severe first: a line reports the first that matches.
var rules = []rule{
	{
		id: "backdoor-panel", sev: High, lang: anyLang,
		title: "Talks to a known backdoor panel (Cipher, Blum): it gives a stranger control of the server",
		re:    regexp.MustCompile(`(?i)cipher-?panel|ciphercheats|blum-?panel`),
		any:   []string{"cipher", "blum"},
	},
	{
		id: "remote-code", sev: High, lang: anyLang,
		title: remoteTitle,
		// One line, as the common loaders are: the request and the load.
		re:  regexp.MustCompile(`(?:PerformHttpRequest|https?\.get|\bfetch\s*\(|\baxios\b).*(?:\bload(?:string)?\s*\(|\beval\s*\(|\bnew\s+Function\s*\()`),
		any: requestWords,
	},
	{
		id: "hidden-call", sev: High, lang: anyLang, hidden: true,
		title: "Hides what it calls in escaped characters, as backdoor loaders do",
		re:    regexp.MustCompile(`PerformHttpRequest|\bload(?:string)?\b|\bassert\b|\bos\.execute\b|\bio\.popen\b|\bExecuteCommand\b|\bGetConvar\b|\b_G\b|\brequire\b|child_process|\beval\b|\bFunction\b`),
	},
	{
		id: "shell", sev: High, lang: luaLang,
		title: "Runs a shell command on the machine",
		re:    regexp.MustCompile(`\b(?:os\.execute|io\.popen)\s*\(`),
		any:   []string{"os.execute", "io.popen"},
	},
	{
		id: "secret-convar", sev: High, lang: anyLang,
		title: "Reads the server's license key, which lets someone run servers as you",
		re:    regexp.MustCompile(`(?i)GetConvar\s*\(\s*['"]sv_licenseKey['"]`),
		any:   []string{"getconvar"},
	},
	{
		id: "encoded-eval", sev: High, lang: jsLang,
		title: "Decodes hidden code and runs it",
		re:    regexp.MustCompile(`(?:\beval|\bnew\s+Function)\s*\(\s*(?:atob|Buffer\.from|unescape|decodeURIComponent)\b`),
		any:   []string{"eval", "function"},
	},
	{
		id: "child-process", sev: Medium, lang: jsLang,
		title: "Can run programs on the machine (honest in build tools such as yarn and webpack)",
		re:    regexp.MustCompile(`require\s*\(\s*['"](?:node:)?child_process['"]\s*\)|from\s+['"](?:node:)?child_process['"]`),
		any:   []string{"child_process"},
	},
	{
		id: "rcon-convar", sev: Medium, lang: anyLang,
		title: "Reads the server's RCON password (honest in admin tools that check it, such as runcode)",
		re:    regexp.MustCompile(`(?i)GetConvar\s*\(\s*['"]rcon_password['"]`),
		any:   []string{"getconvar"},
	},
	{
		id: "obfuscated", sev: Medium, lang: anyLang, raw: true,
		title: "Obfuscated code: what it does is hidden",
		// 40 escaped bytes in a row, or a JavaScript obfuscator's names.
		re:  regexp.MustCompile(`(?:\\x[0-9a-fA-F]{2}|\\[0-9]{1,3}){40}|(?:_0x[0-9a-f]{4,6}\b.*){10}`),
		any: []string{`\`, "_0x"},
	},
	{
		id: "grant-admin", sev: Medium, lang: anyLang,
		title: "Gives someone admin rights from code",
		re:    regexp.MustCompile(`ExecuteCommand\s*\(\s*['"\x60]\s*add_(?:principal|ace)\b`),
		any:   []string{"add_"},
	},
	{
		id: "run-string", sev: Low, lang: luaLang,
		title: "Runs code from a string (honest for config loaders; check where the string comes from)",
		re:    regexp.MustCompile(`\bloadstring\s*\(|\bassert\s*\(\s*load\s*\(`),
		any:   []string{"load"},
	},
}

const remoteTitle = "Downloads code from the internet and runs it"

// remoteWindow is how many lines after a web request a load or eval
// counts as running what it downloaded.
const remoteWindow = 15

var (
	requestWords = []string{"performhttprequest", "http", "fetch", "axios"}
	runWords     = []string{"load", "eval", "function"}
	requestRe    = regexp.MustCompile(`PerformHttpRequest|https?\.get\s*\(|\bfetch\s*\(|\baxios\b`)
	runRe        = regexp.MustCompile(`\bload(?:string)?\s*\(|\beval\s*\(|\bnew\s+Function\s*\(`)
)

// excerptLen is the most of a line a finding shows.
const excerptLen = 160

// Scan checks one file's contents. name is its path, for the findings and
// its language; files that aren't text (escrowed resources) give none.
func Scan(name string, data []byte) []Finding {
	l := langOf(name)
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return nil
	}
	var out []Finding
	lastRequest := -remoteWindow - 1
	for i, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		dec := decode(raw, l)
		lower := strings.ToLower(dec)
		f, ok := match(raw, dec, lower, l)
		if !ok || f.Severity != High {
			// A load a few lines after a request: the multi-line loader.
			if holds(lower, requestWords) && requestRe.MatchString(dec) {
				lastRequest = i
			} else if i-lastRequest <= remoteWindow && holds(lower, runWords) {
				if loc := runRe.FindStringIndex(dec); loc != nil {
					f, ok = Finding{Rule: "remote-code", Severity: High, Title: remoteTitle, Excerpt: excerpt(dec, loc)}, true
				}
			}
		}
		if !ok {
			continue
		}
		f.File, f.Line = name, i+1
		out = append(out, f)
	}
	return out
}

// holds reports whether s holds one of words.
func holds(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func match(raw, dec, lower string, l lang) (Finding, bool) {
	for _, r := range rules {
		if r.lang != anyLang && r.lang != l || r.hidden && dec == raw {
			continue
		}
		s, words := dec, lower
		if r.raw {
			s, words = raw, raw
		}
		if r.any != nil && !holds(words, r.any) {
			continue
		}
		loc := r.re.FindStringIndex(s)
		if loc == nil {
			continue
		}
		if r.hidden && hiddenShown(raw, r.re, dec) {
			continue
		}
		return Finding{Rule: r.id, Severity: r.sev, Title: r.title, Excerpt: excerpt(s, loc)}, true
	}
	return Finding{}, false
}

// hiddenShown reports whether every name re finds in the decoded line is
// also in the line as written (so nothing was hidden).
func hiddenShown(raw string, re *regexp.Regexp, dec string) bool {
	for _, m := range re.FindAllString(dec, -1) {
		if !strings.Contains(raw, m) {
			return false
		}
	}
	return true
}

func excerpt(s string, loc []int) string {
	start := max(0, loc[0]-excerptLen/4)
	end := min(len(s), start+excerptLen)
	out := strings.TrimSpace(strings.ToValidUTF8(s[start:end], "?"))
	// Control characters a decoded line can hold would garble the page.
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, out)
}

func langOf(name string) lang {
	switch strings.ToLower(path.Ext(name)) {
	case ".lua":
		return luaLang
	case ".js", ".cjs", ".mjs":
		return jsLang
	}
	return anyLang
}

var (
	charCodes = regexp.MustCompile(`(?:string\.char|String\.fromCharCode)\s*\(\s*((?:\d{1,5}\s*,\s*)*\d{1,5})\s*\)`)
	hexEsc    = regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)
	decEsc    = regexp.MustCompile(`\\(\d{1,3})`)
	uniEsc    = regexp.MustCompile(`\\u(?:\{([0-9a-fA-F]{1,6})\}|([0-9a-fA-F]{4}))`)
)

// decode spells out what a line hides in escapes ("\x6c\x6f\x61\x64"),
// Lua's decimal escapes ("\108\111"), and character codes
// (string.char(108, 111), String.fromCharCode), so the rules see the names.
func decode(s string, l lang) string {
	if !strings.Contains(s, `\`) && !strings.Contains(s, "har(") && !strings.Contains(s, "CharCode") {
		return s
	}
	s = charCodes.ReplaceAllStringFunc(s, func(m string) string {
		args := charCodes.FindStringSubmatch(m)[1]
		var b strings.Builder
		b.WriteByte('"')
		for a := range strings.SplitSeq(args, ",") {
			n, err := strconv.ParseUint(strings.TrimSpace(a), 10, 21)
			if err != nil || n > 0x10ffff {
				return m
			}
			b.WriteRune(rune(n))
		}
		b.WriteByte('"')
		return b.String()
	})
	s = hexEsc.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.ParseUint(m[2:], 16, 8)
		return string(rune(n))
	})
	if l != jsLang {
		s = decEsc.ReplaceAllStringFunc(s, func(m string) string {
			n, err := strconv.ParseUint(m[1:], 10, 8) // over 255: not a byte
			if err != nil {
				return m
			}
			return string(rune(n))
		})
	}
	if l != luaLang {
		s = uniEsc.ReplaceAllStringFunc(s, func(m string) string {
			n, err := strconv.ParseUint(strings.Trim(m[2:], "{}"), 16, 32)
			if err != nil || n > 0x10ffff {
				return m
			}
			return string(rune(n))
		})
	}
	return s
}
