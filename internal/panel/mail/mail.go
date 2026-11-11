// Package mail is the Panel's email: what one says, written once and sent as
// both plain text and HTML in Raptor's colours (docs/DECISIONS.md #346).
package mail

import (
	"bytes"
	"context"
	_ "embed"
	"html/template"
	"strings"
)

// Sender sends an email to one address.
type Sender interface {
	Send(ctx context.Context, to string, e Email) error
}

// Email is one email. Every part but Subject and Title is optional, and the
// parts appear in this order.
type Email struct {
	Subject string
	// Title is the heading, the first thing read.
	Title string
	// Code is a one-time code, shown large.
	Code string
	// Body is paragraphs of plain text.
	Body []string
	// Facts are labelled values, as a table (a server, a node, a time).
	Facts []Fact
	// Button is the one thing to do, if there is one. Its address is shown
	// under it too, so it can be checked before it's followed.
	Button *Link
	// After is paragraphs after the button.
	After []string
	// Note is the small print at the end: what to do if this wasn't you, or
	// why this email came.
	Note string
}

// Fact is a labelled value.
type Fact struct{ Label, Value string }

// Link is a button.
type Link struct{ Label, URL string }

// Logo is the favicon's tile (docs/DESIGN.md#7-brand), sent inline as LogoID:
// nothing is fetched to show it, so opening an email tells nobody anything.
//
//go:embed logo.png
var Logo []byte

// LogoID is the logo's content ID: the HTML shows cid:LogoID.
const LogoID = "raptor-logo"

// Text is the plain-text version.
func (e Email) Text() string {
	var b strings.Builder
	para := func(s string) {
		if s != "" {
			b.WriteString(s + "\n\n")
		}
	}
	para(e.Title)
	para(e.Code)
	for _, p := range e.Body {
		para(p)
	}
	if len(e.Facts) > 0 {
		for _, f := range e.Facts {
			b.WriteString(f.Label + ": " + f.Value + "\n")
		}
		b.WriteString("\n")
	}
	if e.Button != nil {
		para(e.Button.Label + ":\n" + e.Button.URL)
	}
	for _, p := range e.After {
		para(p)
	}
	para(e.Note)
	b.WriteString("Raptor · https://raptorpanel.net\n")
	return b.String()
}

// HTML is the HTML version: tables and inline styles, which is what every
// mail client draws the same, with dark colours where the client has them.
func (e Email) HTML() string {
	var b bytes.Buffer
	if err := page.Execute(&b, e); err != nil {
		// Only a template bug: the text version still says it all.
		return ""
	}
	return b.String()
}

// Preheader is the line an inbox shows after the subject.
func (e Email) Preheader() string {
	if len(e.Body) > 0 {
		return e.Body[0]
	}
	return e.Title
}

// Daylight and Night (web/src/index.css), as hex for mail clients.
var page = template.Must(template.New("email").Funcs(template.FuncMap{
	"lines": func(s string) []string { return strings.Split(s, "\n") },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>{{.Subject}}</title>
<style>
  body { margin: 0; padding: 0; }
  a { color: #d01025; }
  @media (prefers-color-scheme: dark) {
    .bg { background: #120b0a !important; }
    .card { background: #1d1413 !important; border-color: #33292a !important; }
    .fg { color: #f4efee !important; }
    .muted { color: #a89b99 !important; }
    .code { background: #120b0a !important; border-color: #33292a !important; color: #f4efee !important; }
    .rule { border-color: #33292a !important; }
    .btn { background: #f05653 !important; }
    .btn a { color: #1b0a09 !important; }
    a { color: #f05653 !important; }
    .muted a { color: #a89b99 !important; }
  }
  @media (max-width: 600px) {
    .card { padding: 24px 20px !important; }
  }
</style>
</head>
<body class="bg" style="margin:0;padding:0;background:#f4efee;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="bg" style="background:#f4efee;">
<tr><td align="center" style="padding:32px 16px;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">
  <tr><td style="padding:0 4px 16px;">
    <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
      <td style="vertical-align:middle;"><img src="cid:raptor-logo" width="28" height="28" alt="" style="display:block;border:0;border-radius:6px;"></td>
      <td class="fg" style="vertical-align:middle;padding-left:10px;font-family:'Hanken Grotesk',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:17px;font-weight:700;color:#190f0e;">Raptor</td>
    </tr></table>
  </td></tr>
  <tr><td class="card" style="background:#ffffff;border:1px solid #e3dcdb;border-radius:12px;padding:32px;font-family:'Hanken Grotesk',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
    <h1 class="fg" style="margin:0 0 16px;font-size:22px;line-height:1.3;font-weight:700;color:#190f0e;">{{.Title}}</h1>
    {{- if .Code}}
    <div class="code" style="margin:0 0 20px;padding:16px;background:#f9f6f5;border:1px solid #e3dcdb;border-radius:8px;text-align:center;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:32px;font-weight:700;letter-spacing:8px;color:#190f0e;">{{.Code}}</div>
    {{- end}}
    {{- range .Body}}
    <p class="fg" style="margin:0 0 16px;font-size:15px;line-height:1.6;color:#190f0e;">{{range $i, $l := lines .}}{{if $i}}<br>{{end}}{{$l}}{{end}}</p>
    {{- end}}
    {{- if .Facts}}
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 20px;font-size:14px;line-height:1.5;">
      {{- range .Facts}}
      <tr>
        <td class="muted rule" style="padding:8px 16px 8px 0;border-top:1px solid #e3dcdb;color:#6e5f5d;white-space:nowrap;vertical-align:top;">{{.Label}}</td>
        <td class="fg rule" style="padding:8px 0;border-top:1px solid #e3dcdb;color:#190f0e;word-break:break-word;">{{.Value}}</td>
      </tr>
      {{- end}}
    </table>
    {{- end}}
    {{- with .Button}}
    <table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:8px 0 12px;"><tr>
      <td class="btn" style="background:#d01025;border-radius:8px;"><a href="{{.URL}}" style="display:inline-block;padding:12px 22px;font-size:15px;font-weight:600;color:#ffffff;text-decoration:none;">{{.Label}}</a></td>
    </tr></table>
    <p class="muted" style="margin:0 0 20px;font-size:12px;line-height:1.5;color:#6e5f5d;word-break:break-all;">{{.URL}}</p>
    {{- end}}
    {{- range .After}}
    <p class="fg" style="margin:0 0 16px;font-size:15px;line-height:1.6;color:#190f0e;">{{range $i, $l := lines .}}{{if $i}}<br>{{end}}{{$l}}{{end}}</p>
    {{- end}}
    {{- if .Note}}
    <p class="muted rule" style="margin:8px 0 0;padding-top:16px;border-top:1px solid #e3dcdb;font-size:13px;line-height:1.5;color:#6e5f5d;">{{.Note}}</p>
    {{- end}}
  </td></tr>
  <tr><td class="muted" style="padding:16px 4px 0;font-family:'Hanken Grotesk',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:12px;line-height:1.5;color:#6e5f5d;">
    Raptor · <a href="https://raptorpanel.net" style="color:#6e5f5d;">raptorpanel.net</a>
  </td></tr>
  </table>
</td></tr>
</table>
</body>
</html>
`))
