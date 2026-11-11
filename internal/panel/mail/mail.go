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
	// Kicker names the context above the title, as the app's pages do
	// ("account", "billing"): HTML only, as "// account".
	Kicker string
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

// Fact is a labelled value. Mono is for data, which the app shows in mono
// (an address, a time, an ID); a sentence isn't.
type Fact struct {
	Label, Value string
	Mono         bool
}

// Link is a button.
type Link struct{ Label, URL string }

// Image is a picture sent inline with every email, shown as cid:ID: nothing
// is fetched to show it, so opening an email tells nobody anything.
type Image struct {
	ID  string
	PNG []byte
}

// The full logo (docs/DESIGN.md#7-brand), the raptor and the wordmark, drawn
// on the card's colour in each theme.
var (
	//go:embed logo-light.png
	logoLight []byte
	//go:embed logo-dark.png
	logoDark []byte
)

// Images are the inline pictures every email's HTML shows.
var Images = []Image{{ID: "raptor-logo", PNG: logoLight}, {ID: "raptor-logo-dark", PNG: logoDark}}

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

// The page is docs/DESIGN.md's in hex, since mail clients don't know oklch:
// Daylight (page #f9f6f5, card #f4efee, rule #ded5d4, Graphite #190f0e,
// Slate #5f5554, Signal Red #d01025) and the Console where a client has a
// dark mode (Ink #0f0808, Raised Ink #181110, rule #342c2b, Bone #f5f0f0,
// Ash #9f9695, Signal Red #f05653). Corners are the app's 4px, and the
// button is its primary key: mono capitals with the key highlight.
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
  body { margin: 0; padding: 0; -webkit-text-size-adjust: 100%; }
  a { color: #d01025; }
  .btn a:hover { background: #b80d20 !important; }
  @media (prefers-color-scheme: dark) {
    .page { background: #0f0808 !important; }
    .card { background: #181110 !important; border-color: #342c2b !important; }
    .fg { color: #f5f0f0 !important; }
    .muted { color: #9f9695 !important; }
    .rule { border-color: #342c2b !important; }
    .code { background: #0f0808 !important; border-color: #342c2b !important; color: #f5f0f0 !important; }
    .btn { background: #f05653 !important; }
    .btn a { background: #f05653 !important; color: #1b0a09 !important; }
    .btn a:hover { background: #f36d69 !important; }
    a { color: #f05653 !important; }
    .muted a { color: #9f9695 !important; }
    .logo-light { display: none !important; }
    .logo-dark { display: block !important; max-height: none !important; }
  }
  @media (max-width: 600px) {
    .card { padding: 24px 20px !important; }
  }
</style>
</head>
<body class="page" style="margin:0;padding:0;background:#f9f6f5;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="page" style="background:#f9f6f5;">
<tr><td align="center" style="padding:32px 16px;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">
  <tr><td style="padding:0 0 20px;">
    <img class="logo-light" src="cid:raptor-logo" width="125" height="20" alt="Raptor" style="display:block;border:0;">
    <!--[if !mso]><!--><img class="logo-dark" src="cid:raptor-logo-dark" width="125" height="20" alt="Raptor" style="display:none;max-height:0;overflow:hidden;border:0;"><!--<![endif]-->
  </td></tr>
  <tr><td class="card" style="background:#f4efee;border:1px solid #ded5d4;border-radius:6px;padding:28px;font-family:'Hanken Grotesk',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
    {{- if .Kicker}}
    <p class="muted" style="margin:0 0 8px;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:11px;line-height:1.4;letter-spacing:0.18em;text-transform:uppercase;color:#5f5554;">// {{.Kicker}}</p>
    {{- end}}
    <h1 class="fg" style="margin:0 0 16px;font-size:24px;line-height:1.333;font-weight:700;letter-spacing:-0.025em;text-wrap:balance;color:#190f0e;">{{.Title}}</h1>
    {{- if .Code}}
    <div class="code" style="margin:0 0 20px;padding:14px 16px;background:#f9f6f5;border:1px solid #ded5d4;border-radius:4px;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:30px;line-height:1.2;font-weight:500;letter-spacing:0.3em;font-variant-numeric:tabular-nums;color:#190f0e;">{{.Code}}</div>
    {{- end}}
    {{- range .Body}}
    <p class="fg" style="margin:0 0 14px;font-size:14px;line-height:1.55;color:#190f0e;">{{range $i, $l := lines .}}{{if $i}}<br>{{end}}{{$l}}{{end}}</p>
    {{- end}}
    {{- if .Facts}}
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="rule" style="margin:6px 0 20px;border-bottom:1px solid #ded5d4;">
      {{- range .Facts}}
      <tr>
        <td class="muted rule" width="96" style="padding:9px 12px 9px 0;border-top:1px solid #ded5d4;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:11px;line-height:20px;letter-spacing:0.025em;text-transform:uppercase;color:#5f5554;white-space:nowrap;vertical-align:top;">{{.Label}}</td>
        {{- if .Mono}}
        <td class="fg rule" style="padding:9px 0;border-top:1px solid #ded5d4;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;line-height:20px;font-variant-numeric:tabular-nums;color:#190f0e;word-break:break-word;">{{.Value}}</td>
        {{- else}}
        <td class="fg rule" style="padding:9px 0;border-top:1px solid #ded5d4;font-size:14px;line-height:20px;color:#190f0e;word-break:break-word;">{{.Value}}</td>
        {{- end}}
      </tr>
      {{- end}}
    </table>
    {{- end}}
    {{- with .Button}}
    <table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:6px 0 10px;"><tr>
      <td class="btn" style="background:#d01025;border-radius:4px;box-shadow:inset 0 1px 0 rgba(255,255,255,0.22);"><a href="{{.URL}}" style="display:inline-block;padding:0 18px;line-height:40px;border-radius:4px;background:#d01025;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12px;font-weight:500;letter-spacing:0.05em;text-transform:uppercase;color:#fff9f8;text-decoration:none;">{{.Label}}</a></td>
    </tr></table>
    <p class="muted" style="margin:0 0 20px;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:11px;line-height:1.5;color:#5f5554;word-break:break-all;">{{.URL}}</p>
    {{- end}}
    {{- range .After}}
    <p class="fg" style="margin:0 0 14px;font-size:14px;line-height:1.55;color:#190f0e;">{{range $i, $l := lines .}}{{if $i}}<br>{{end}}{{$l}}{{end}}</p>
    {{- end}}
    {{- if .Note}}
    <p class="muted rule" style="margin:6px 0 0;padding-top:16px;border-top:1px solid #ded5d4;font-size:13px;line-height:1.5;color:#5f5554;">{{.Note}}</p>
    {{- end}}
  </td></tr>
  <tr><td class="muted" style="padding:16px 0 0;font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:11px;line-height:1.5;letter-spacing:0.025em;color:#5f5554;">
    Raptor · <a href="https://raptorpanel.net" style="color:#5f5554;">raptorpanel.net</a>
  </td></tr>
  </table>
</td></tr>
</table>
</body>
</html>
`))
