package hosted

import "testing"

func TestPrefix(t *testing.T) {
	p := Prefix("o1", "n1")
	if p != "orgs/o1/nodes/n1/" {
		t.Fatal(p)
	}
	if n, ok := NodeOf(p); !ok || n != "n1" {
		t.Fatalf("NodeOf = %q, %v", n, ok)
	}
	for _, bad := range []string{"orgs/o1/", "orgs/o1/nodes/n1", "orgs//nodes/n1/", "orgs/o1/nodes/n1/x/", "x/o1/nodes/n1/"} {
		if _, ok := NodeOf(bad); ok {
			t.Errorf("NodeOf(%q) accepted", bad)
		}
	}
	for e, want := range map[string]bool{
		"s3.us-west-004.backblazeb2.com":          true,
		"s3.eu-central-003.backblazeb2.com":       true,
		"s3.evil.com":                             false,
		"s3.us-west-004.backblazeb2.com.evil.com": false,
		"https://s3.us-west-004.backblazeb2.com":  false,
	} {
		if Endpoint(e) != want {
			t.Errorf("Endpoint(%q) = %v", e, !want)
		}
	}
}
