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
