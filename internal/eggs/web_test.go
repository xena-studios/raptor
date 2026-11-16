package eggs

import "testing"

func TestCheckWeb(t *testing.T) {
	vars := []Variable{{Env: "TXHOST_TXA_PORT"}, {Env: "TXADMIN_ENABLE"}}
	for _, w := range []*Web{nil, {Name: "Grafana"}, {Name: "txAdmin", Port: "TXHOST_TXA_PORT", Enabled: "TXADMIN_ENABLE"}} {
		if err := CheckWeb(w, vars); err != nil {
			t.Errorf("%+v: %v", w, err)
		}
	}
	for name, w := range map[string]*Web{
		"no name":        {Port: "TXHOST_TXA_PORT"},
		"unknown port":   {Name: "x", Port: "PORT"},
		"unknown switch": {Name: "x", Enabled: "ON"},
	} {
		if err := CheckWeb(w, vars); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCheckScan(t *testing.T) {
	for _, s := range []*Scan{nil, {Rules: "fivem", Dirs: []string{"resources", "txData"}}, {Rules: "fivem", Dirs: []string{"a/b"}}} {
		if err := CheckScan(s); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	for name, s := range map[string]*Scan{
		"unknown rules": {Rules: "minecraft", Dirs: []string{"plugins"}},
		"no dirs":       {Rules: "fivem"},
		"absolute":      {Rules: "fivem", Dirs: []string{"/etc"}},
		"climbs":        {Rules: "fivem", Dirs: []string{"../x"}},
		"the root":      {Rules: "fivem", Dirs: []string{"."}},
		"unclean":       {Rules: "fivem", Dirs: []string{"a//b"}},
		"empty":         {Rules: "fivem", Dirs: []string{""}},
	} {
		if err := CheckScan(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
