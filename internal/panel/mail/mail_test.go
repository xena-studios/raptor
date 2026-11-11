package mail

import (
	"strings"
	"testing"
)

func TestEmail(t *testing.T) {
	e := Email{
		Subject: "Join <Acme> on Raptor",
		Title:   "Join <Acme> on Raptor",
		Code:    "123456",
		Body:    []string{"first <b>not bold</b>", "two\nlines"},
		Facts:   []Fact{{Label: "Server", Value: "rust"}, {Label: "Node", Value: "box-1"}},
		Button:  &Link{Label: "Accept", URL: "https://app.example/invite#tok"},
		After:   []string{"after"},
		Note:    "If this wasn't you, ignore it.",
	}
	want := "Join <Acme> on Raptor\n\n123456\n\nfirst <b>not bold</b>\n\ntwo\nlines\n\nServer: rust\nNode: box-1\n\nAccept:\nhttps://app.example/invite#tok\n\nafter\n\nIf this wasn't you, ignore it.\n\nRaptor · https://raptorpanel.net\n"
	if got := e.Text(); got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}

	html := e.HTML()
	for _, s := range []string{
		"Join &lt;Acme&gt; on Raptor", "first &lt;b&gt;not bold&lt;/b&gt;", "two<br>lines",
		`href="https://app.example/invite#tok"`, ">123456<", ">rust<", `src="cid:raptor-logo"`, `src="cid:raptor-logo-dark"`,
		"If this wasn&#39;t you",
	} {
		if !strings.Contains(html, s) {
			t.Errorf("html has no %q", s)
		}
	}
	if strings.Contains(html, "<b>") {
		t.Error("html: text wasn't escaped")
	}

	// A link that isn't one: html/template won't make it a href.
	bad := Email{Subject: "s", Title: "t", Button: &Link{Label: "x", URL: "javascript:alert(1)"}}
	if strings.Contains(bad.HTML(), `href="javascript:`) {
		t.Error("a javascript: link was kept")
	}

	// The parts are optional.
	if got := (Email{Subject: "s", Title: "t"}).Text(); got != "t\n\nRaptor · https://raptorpanel.net\n" {
		t.Errorf("title only: %q", got)
	}
	for _, img := range Images {
		if len(img.PNG) == 0 {
			t.Errorf("no %s", img.ID)
		}
	}
}
