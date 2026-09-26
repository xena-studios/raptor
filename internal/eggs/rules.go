package eggs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Variable rules are Laravel validation rules, because that's what Pterodactyl
// and Pelican validate them with. This implements the rules real eggs use
// (surveyed across ~600 community eggs) with Laravel's semantics:
//
//   - An empty value (after trimming) is null, as Laravel's middleware makes
//     it. With "nullable", null passes every rule. Without it, null fails
//     "required" and every type rule (string, integer, numeric, boolean,
//     regex, …), and counts as length 0 for size rules.
//   - Size rules (min, max, between, size, gt, …) compare numbers when the
//     variable also has a numeric rule (numeric, integer) and the value is
//     numeric; otherwise they compare the string length in characters.
//   - "regex:" uses PHP (PCRE) syntax with delimiters and is translated to Go.
//
// Rules Wings doesn't know (and PCRE patterns Go can't run) are skipped, not
// failed, so an egg never becomes unusable; Egg.Lint reports them.

// VariableError is one failed rule.
type VariableError struct {
	Env     string // the variable's environment name
	Name    string // the variable's display name
	Rule    string
	Message string
}

func (e VariableError) Error() string { return e.Env + ": " + e.Message }

// VariableErrors is returned by Validate when any variable fails its rules.
type VariableErrors []VariableError

func (e VariableErrors) Error() string {
	msgs := make([]string, len(e))
	for i, v := range e {
		msgs[i] = v.Error()
	}
	return "invalid variables: " + strings.Join(msgs, "; ")
}

// Validate checks values against the egg's variable rules and returns the
// complete set: every egg variable, with its default where values has none.
// Keys that aren't egg variables are ignored. On failure the error is a
// VariableErrors.
func (e *Egg) Validate(values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(e.Variables))
	var errs VariableErrors
	for _, v := range e.Variables {
		val, ok := values[v.Env]
		if !ok {
			val = v.Default
		}
		out[v.Env] = val
		errs = append(errs, v.check(val)...)
	}
	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

// Lint reports rules Validate can't enforce: unknown rule names and regex
// patterns that don't translate to Go.
func (e *Egg) Lint() []string {
	var out []string
	for _, v := range e.Variables {
		for _, r := range v.Rules {
			name, param, _ := strings.Cut(r, ":")
			switch {
			case !knownRule(name):
				out = append(out, fmt.Sprintf("%s: unsupported rule %q (ignored)", v.Env, r))
			case name == "regex" || name == "not_regex":
				if _, err := phpRegex(param); err != nil {
					out = append(out, fmt.Sprintf("%s: %s pattern can't be checked (%v); rule ignored", v.Env, name, err))
				}
			}
		}
	}
	return out
}

func knownRule(name string) bool {
	switch name {
	case "required", "nullable", "sometimes", "present", "filled", "bail",
		"string", "integer", "int", "numeric", "boolean", "bool",
		"in", "not_in", "min", "max", "between", "size", "gt", "gte", "lt", "lte",
		"digits", "digits_between", "regex", "not_regex", "alpha", "alpha_num", "alpha_dash",
		"url", "ip", "ipv4", "ipv6", "starts_with", "ends_with", "lowercase", "uppercase",
		"json", "uuid":
		return true
	}
	return false
}

var (
	reAlpha     = regexp.MustCompile(`^[\pL\pM]+$`)
	reAlphaNum  = regexp.MustCompile(`^[\pL\pM\pN]+$`)
	reAlphaDash = regexp.MustCompile(`^[\pL\pM\pN_-]+$`)
	reInteger   = regexp.MustCompile(`^\s*[+-]?(0|[1-9][0-9]*)\s*$`)
	reNumeric   = regexp.MustCompile(`^\s*[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?\s*$`)
	reDigits    = regexp.MustCompile(`^[0-9]+$`)
	reUUID      = regexp.MustCompile(`^[\da-fA-F]{8}-[\da-fA-F]{4}-[\da-fA-F]{4}-[\da-fA-F]{4}-[\da-fA-F]{12}$`)
)

// check returns the rules val fails.
func (v Variable) check(raw string) []VariableError {
	val := strings.TrimSpace(raw)
	null := val == ""
	has := func(names ...string) bool {
		return slices.ContainsFunc(v.Rules, func(r string) bool {
			name, _, _ := strings.Cut(r, ":")
			return slices.Contains(names, name)
		})
	}
	if null && has("nullable") {
		return nil
	}
	numericRules := has("numeric", "integer", "int")

	// size is Laravel's getSize: the number itself for numeric values of
	// numeric variables, otherwise the length in characters.
	size := func() *big.Float {
		if numericRules && reNumeric.MatchString(val) {
			if f, ok := new(big.Float).SetString(strings.TrimSpace(val)); ok {
				return f
			}
		}
		return new(big.Float).SetInt64(int64(utf8.RuneCountInString(val)))
	}
	num := func(s string) *big.Float {
		f, ok := new(big.Float).SetString(strings.TrimSpace(s))
		if !ok {
			return nil
		}
		return f
	}

	var errs []VariableError
	fail := func(rule, format string, a ...any) {
		errs = append(errs, VariableError{Env: v.Env, Name: v.Name, Rule: rule, Message: fmt.Sprintf(format, a...)})
	}

	for _, rule := range v.Rules {
		name, param, _ := strings.Cut(rule, ":")
		params := func() []string { return csv(param) }
		switch name {
		case "required", "filled":
			if null {
				fail(rule, "is required")
			}
		case "string":
			if null {
				fail(rule, "must be a string")
			}
		case "integer", "int":
			if null || !reInteger.MatchString(val) {
				fail(rule, "must be an integer")
			} else if _, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err != nil {
				fail(rule, "must be an integer")
			}
		case "numeric":
			if null || !reNumeric.MatchString(val) {
				fail(rule, "must be a number")
			}
		case "boolean", "bool":
			// Laravel accepts real booleans and 0/1, but variable values are
			// strings, so of those only "0" and "1" pass (not "true"/"false").
			if val != "0" && val != "1" {
				fail(rule, "must be 1 or 0")
			}
		case "in":
			if !slices.Contains(params(), val) {
				fail(rule, "must be one of: %s", strings.Join(params(), ", "))
			}
		case "not_in":
			if slices.Contains(params(), val) {
				fail(rule, "must not be one of: %s", strings.Join(params(), ", "))
			}
		case "min", "max", "size", "gt", "gte", "lt", "lte":
			p := num(param)
			if p == nil {
				continue
			}
			c := size().Cmp(p)
			ok := map[string]bool{"min": c >= 0, "max": c <= 0, "size": c == 0, "gt": c > 0, "gte": c >= 0, "lt": c < 0, "lte": c <= 0}[name]
			if !ok {
				unit := ""
				if !numericRules || !reNumeric.MatchString(val) {
					unit = " characters"
				}
				fail(rule, "must be %s %s%s", map[string]string{"min": "at least", "max": "at most", "size": "exactly", "gt": "more than", "gte": "at least", "lt": "less than", "lte": "at most"}[name], param, unit)
			}
		case "between":
			p := params()
			if len(p) != 2 {
				continue
			}
			lo, hi := num(p[0]), num(p[1])
			if lo == nil || hi == nil {
				continue
			}
			if s := size(); s.Cmp(lo) < 0 || s.Cmp(hi) > 0 {
				fail(rule, "must be between %s and %s", p[0], p[1])
			}
		case "digits", "digits_between":
			if null || !reDigits.MatchString(val) {
				fail(rule, "must be digits")
				continue
			}
			n := len(val)
			p := params()
			if name == "digits" && len(p) == 1 && strconv.Itoa(n) != p[0] {
				fail(rule, "must be %s digits", p[0])
			}
			if name == "digits_between" && len(p) == 2 {
				lo, _ := strconv.Atoi(p[0])
				hi, _ := strconv.Atoi(p[1])
				if n < lo || n > hi {
					fail(rule, "must be between %s and %s digits", p[0], p[1])
				}
			}
		case "regex", "not_regex":
			re, err := phpRegex(param)
			if err != nil {
				continue // reported by Lint
			}
			if null || re.MatchString(val) != (name == "regex") {
				fail(rule, "has an invalid format")
			}
		case "alpha":
			if null || !reAlpha.MatchString(val) {
				fail(rule, "must contain only letters")
			}
		case "alpha_num":
			if null || !reAlphaNum.MatchString(val) {
				fail(rule, "must contain only letters and numbers")
			}
		case "alpha_dash":
			if null || !reAlphaDash.MatchString(val) {
				fail(rule, "must contain only letters, numbers, dashes, and underscores")
			}
		case "url":
			if u, err := url.Parse(val); null || err != nil || u.Scheme == "" || u.Host == "" {
				fail(rule, "must be a valid URL")
			}
		case "ip", "ipv4", "ipv6":
			a, err := netip.ParseAddr(val)
			if null || err != nil || a.Zone() != "" || (name == "ipv4" && !a.Is4()) || (name == "ipv6" && !a.Is6()) {
				fail(rule, "must be a valid IP address")
			}
		case "starts_with", "ends_with":
			has := strings.HasPrefix
			if name == "ends_with" {
				has = strings.HasSuffix
			}
			if !slices.ContainsFunc(params(), func(p string) bool { return has(val, p) }) {
				fail(rule, "must %s one of: %s", strings.ReplaceAll(name, "_", " "), strings.Join(params(), ", "))
			}
		case "lowercase", "uppercase":
			want := strings.ToLower(val)
			if name == "uppercase" {
				want = strings.ToUpper(val)
			}
			if null || val != want {
				fail(rule, "must be %s", name)
			}
		case "json":
			if null || !json.Valid([]byte(val)) {
				fail(rule, "must be valid JSON")
			}
		case "uuid":
			if null || !reUUID.MatchString(val) {
				fail(rule, "must be a valid UUID")
			}
		}
	}
	return errs
}

// csv splits rule parameters the way Laravel does (str_getcsv): commas
// separate values, and double quotes may wrap a value containing commas.
func csv(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && quoted && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			i++
		case c == '"':
			quoted = !quoted
		case c == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	for i := range out {
		out[i] = strings.TrimSpace(out[i]) // "in: a,b" is common in eggs
	}
	return out
}

var closingDelim = map[byte]byte{'(': ')', '[': ']', '{': '}', '<': '>'}

// phpRegex translates a PHP preg pattern ("/^a+$/i") to Go. PHP allows any
// non-alphanumeric delimiter, including bracket pairs ("(a+)"), followed by
// modifiers. Patterns using PCRE-only features (lookarounds, backreferences,
// possessive quantifiers) fail to compile and are reported as errors.
func phpRegex(p string) (*regexp.Regexp, error) {
	p = strings.TrimLeft(p, " \t\n\r")
	if p == "" {
		return nil, errors.New("empty pattern")
	}
	open := p[0]
	if open == '\\' || open >= 0x80 || (open >= '0' && open <= '9') || (open|0x20 >= 'a' && open|0x20 <= 'z') {
		return nil, fmt.Errorf("invalid delimiter %q", open)
	}
	closeCh := open
	if c, ok := closingDelim[open]; ok {
		closeCh = c
	}
	end := strings.LastIndexByte(p, closeCh)
	if end <= 0 {
		return nil, errors.New("no closing delimiter")
	}
	pattern, mods := p[1:end], p[end+1:]
	var flags string
	anchored := false
	for _, m := range mods {
		switch m {
		case 'i', 'm', 's', 'U':
			flags += string(m)
		case 'u', 'D', 'S':
			// UTF-8 is Go's default; D and S don't change what matches here.
		case 'A':
			anchored = true
		default:
			return nil, fmt.Errorf("unsupported modifier %q", m)
		}
	}
	if anchored {
		pattern = `\A(?:` + pattern + `)`
	}
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}
	return regexp.Compile(pattern)
}
