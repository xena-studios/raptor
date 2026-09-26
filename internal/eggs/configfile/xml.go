package configfile

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// XML files are parsed into a small tree that keeps every token (comments,
// processing instructions, whitespace, attribute order), so only the edited
// elements change. Pterodactyl re-indents the whole document.
//
// Rule keys are element paths from the root element. Eggs write them with
// dots ("MyConfigDedicated.SessionSettings.MaxPlayers"), which Pterodactyl
// turns into slashes; slashes work too. A segment can be "*" or carry a
// predicate: [@attr='value'], [@attr], or a 1-based position [2].
//
// A value of the form [attr='value'] sets an attribute instead of the text
// (7 Days to Die: property[@name='ServerPort'] = [value='26900']). Missing
// elements are created for paths without wildcards.

type xnode struct {
	kind     int // xElem, xText, xComment, xProcInst, xDirective
	name     xml.Name
	attrs    []xml.Attr
	children []*xnode
	text     string // text, comment, directive
	target   string // processing instruction
}

const (
	xElem = iota
	xText
	xComment
	xProcInst
	xDirective
)

const maxXMLDepth = 512

var xmlAttrValue = regexp.MustCompile(`^\[([\w:.-]+)='(.*)'\]$`)

func editXML(data []byte, rules []Rule) ([]byte, error) {
	bom := bytes.HasPrefix(data, []byte("\xef\xbb\xbf"))
	doc, err := parseXML(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		segs := xmlPath(r.Key)
		if len(segs) == 0 {
			continue
		}
		if rootElem(doc) == nil {
			if !validXMLName(segs[0].name) {
				continue
			}
			if len(doc.children) == 0 {
				doc.children = append(doc.children,
					&xnode{kind: xProcInst, target: "xml", text: `version="1.0" encoding="utf-8"`},
					&xnode{kind: xText, text: "\n"})
			}
			doc.children = append(doc.children, &xnode{kind: xElem, name: xml.Name{Local: segs[0].name}})
		}
		create := r.IfValue == "" && !strings.Contains(r.Key, "*")
		for _, el := range findXML(doc, segs, create) {
			setXML(el, r)
		}
	}
	var b bytes.Buffer
	if bom {
		b.WriteString("\xef\xbb\xbf")
	}
	for _, c := range doc.children {
		writeXML(&b, c)
	}
	return b.Bytes(), nil
}

func rootElem(doc *xnode) *xnode {
	for _, c := range doc.children {
		if c.kind == xElem {
			return c
		}
	}
	return nil
}

func parseXML(data []byte) (*xnode, error) {
	doc := &xnode{kind: xElem}
	stack := []*xnode{doc}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) > maxXMLDepth {
				return nil, errors.New("XML nested too deeply")
			}
			el := &xnode{kind: xElem, name: t.Name, attrs: append([]xml.Attr(nil), t.Attr...)}
			top.children = append(top.children, el)
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) == 1 || top.name != t.Name {
				return nil, errors.New("mismatched end element </" + qname(t.Name) + ">")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.children = append(top.children, &xnode{kind: xText, text: string(t)})
		case xml.Comment:
			top.children = append(top.children, &xnode{kind: xComment, text: string(t)})
		case xml.ProcInst:
			top.children = append(top.children, &xnode{kind: xProcInst, target: t.Target, text: string(t.Inst)})
		case xml.Directive:
			top.children = append(top.children, &xnode{kind: xDirective, text: string(t)})
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("unclosed element <" + qname(stack[len(stack)-1].name) + ">")
	}
	elems := 0
	for _, c := range doc.children {
		if c.kind == xElem {
			elems++
		} else if c.kind == xText && strings.TrimSpace(c.text) != "" {
			return nil, errors.New("text outside the root element")
		}
	}
	if elems > 1 {
		return nil, errors.New("more than one root element")
	}
	return doc, nil
}

type xseg struct {
	name  string
	attr  string // predicate attribute name
	value string // predicate attribute value
	hasEq bool
	pos   int // 1-based position predicate, 0 = none
}

// xmlPath splits a rule key into segments. Dots outside predicates are
// separators, like slashes.
func xmlPath(key string) []xseg {
	key = strings.TrimPrefix(key, "./")
	key = strings.TrimPrefix(key, "/")
	var parts []string
	var cur strings.Builder
	depth := 0
	quote := byte(0)
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			if depth > 0 {
				quote = c
			}
		case c == '[':
			depth++
		case c == ']':
			depth--
		case (c == '.' || c == '/') && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	parts = append(parts, cur.String())

	out := make([]xseg, 0, len(parts))
	for _, p := range parts {
		s := xseg{name: p}
		if i := strings.IndexByte(p, '['); i > 0 && strings.HasSuffix(p, "]") {
			s.name = p[:i]
			pred := p[i+1 : len(p)-1]
			if n, err := strconv.Atoi(pred); err == nil && n > 0 {
				s.pos = n
			} else if a, ok := strings.CutPrefix(pred, "@"); ok {
				if name, val, eq := strings.Cut(a, "="); eq {
					s.attr, s.hasEq = strings.TrimSpace(name), true
					val = strings.TrimSpace(val)
					if len(val) >= 2 && (val[0] == '\'' || val[0] == '"') && val[len(val)-1] == val[0] {
						val = val[1 : len(val)-1]
					}
					s.value = val
				} else {
					s.attr = strings.TrimSpace(a)
				}
			}
		}
		if s.name == "" {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func (s xseg) matches(n *xnode) bool {
	if n.kind != xElem {
		return false
	}
	if s.name != "*" && s.name != qname(n.name) && s.name != n.name.Local {
		return false
	}
	if s.attr != "" {
		v, ok := attr(n, s.attr)
		if !ok || (s.hasEq && v != s.value) {
			return false
		}
	}
	return true
}

// findXML returns the elements a path selects, creating missing ones when
// create is set (only for plain names and [@attr='value'] predicates).
func findXML(doc *xnode, segs []xseg, create bool) []*xnode {
	cur := []*xnode{doc}
	for _, s := range segs {
		var next []*xnode
		for _, parent := range cur {
			var found []*xnode
			for _, c := range parent.children {
				if s.matches(c) {
					found = append(found, c)
				}
			}
			if s.pos > 0 {
				if s.pos <= len(found) {
					found = found[s.pos-1 : s.pos]
				} else {
					found = nil
				}
			}
			if len(found) == 0 && create && parent != doc && validXMLName(s.name) && s.pos == 0 && (s.attr == "" || (s.hasEq && validXMLName(s.attr))) {
				el := &xnode{kind: xElem, name: xml.Name{Local: s.name}}
				if s.attr != "" {
					el.attrs = []xml.Attr{{Name: xml.Name{Local: s.attr}, Value: s.value}}
				}
				appendChild(parent, el)
				found = []*xnode{el}
			}
			next = append(next, found...)
		}
		cur = next
	}
	return cur
}

// appendChild adds el to parent, indented like its existing children.
func appendChild(parent, el *xnode) {
	var indent, closing *xnode
	for i, c := range parent.children {
		if c.kind == xElem && i > 0 && parent.children[i-1].kind == xText && strings.TrimSpace(parent.children[i-1].text) == "" {
			indent = parent.children[i-1]
			break
		}
	}
	if n := len(parent.children); n > 0 {
		if last := parent.children[n-1]; last.kind == xText && strings.TrimSpace(last.text) == "" {
			closing = last
			parent.children = parent.children[:n-1]
		}
	}
	if indent != nil {
		parent.children = append(parent.children, &xnode{kind: xText, text: indent.text})
	}
	parent.children = append(parent.children, el)
	if closing != nil {
		parent.children = append(parent.children, closing)
	}
}

func setXML(el *xnode, r Rule) {
	value := r.String()
	if m := xmlAttrValue.FindStringSubmatch(value); m != nil {
		name, v := m[1], m[2]
		cur, _ := attr(el, name)
		ok, replaced := r.match(cur)
		if !ok {
			return
		}
		if strings.HasPrefix(r.IfValue, "regex:") {
			v = xmlAttrValue.ReplaceAllString(replaced, "$2")
		}
		for i := range el.attrs {
			if qname(el.attrs[i].Name) == name {
				el.attrs[i].Value = v
				return
			}
		}
		if validXMLName(name) {
			el.attrs = append(el.attrs, xml.Attr{Name: xml.Name{Local: name}, Value: v})
		}
		return
	}

	// Replace the element's leading text, keeping child elements.
	i := 0
	var cur strings.Builder
	for i < len(el.children) && el.children[i].kind == xText {
		cur.WriteString(el.children[i].text)
		i++
	}
	ok, replaced := r.match(cur.String())
	if !ok {
		return
	}
	if strings.HasPrefix(r.IfValue, "regex:") {
		value = replaced
	}
	el.children = append([]*xnode{{kind: xText, text: value}}, el.children[i:]...)
}

// validXMLName reports whether s can be written as an element or attribute
// name ("name" or "prefix:name"), so a rule can never produce a document
// that doesn't parse. Go's own XML reader decides.
func validXMLName(s string) bool {
	if s == "" || strings.Count(s, ":") > 1 || strings.HasPrefix(s, ":") || strings.HasSuffix(s, ":") ||
		strings.ContainsAny(s, " \t\r\n<>/=\"'&") {
		return false
	}
	tok, err := xml.NewDecoder(strings.NewReader("<" + s + "/>")).RawToken()
	start, ok := tok.(xml.StartElement)
	return err == nil && ok && qname(start.Name) == s
}

func attr(n *xnode, name string) (string, bool) {
	for _, a := range n.attrs {
		if qname(a.Name) == name || a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#xA;", "\r", "&#xD;", "\t", "&#x9;")
)

// xmlChars drops characters XML 1.0 can't contain (invalid UTF-8, most
// control characters), so a value can never break the document.
func xmlChars(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == utf8.RuneError, r < 0x20 && r != '\t' && r != '\n' && r != '\r', r == 0xFFFE, r == 0xFFFF:
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

func writeXML(b *bytes.Buffer, n *xnode) {
	switch n.kind {
	case xText:
		b.WriteString(textEscaper.Replace(xmlChars(n.text)))
	case xComment:
		b.WriteString("<!--" + n.text + "-->")
	case xProcInst:
		b.WriteString("<?" + n.target)
		if n.text != "" {
			b.WriteString(" " + n.text)
		}
		b.WriteString("?>")
	case xDirective:
		b.WriteString("<!" + n.text + ">")
	case xElem:
		b.WriteString("<" + qname(n.name))
		for _, a := range n.attrs {
			b.WriteString(" " + qname(a.Name) + `="` + attrEscaper.Replace(xmlChars(a.Value)) + `"`)
		}
		if len(n.children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
		for _, c := range n.children {
			writeXML(b, c)
		}
		b.WriteString("</" + qname(n.name) + ">")
	}
}
