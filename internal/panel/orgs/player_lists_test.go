package orgs

import "testing"

func TestPlayerLists(t *testing.T) {
	for egg, want := range map[string]bool{
		`{"players":"minecraft","addons":{"plugins":{"loaders":["paper"]}}}`: true,
		`{"players":"minecraft"}`: true,
		`{"players":"minecraft","addons":{"plugins":{"loaders":["velocity"]}}}`:               false,
		`{"players":"minecraft","addons":{"plugins":{"loaders":["waterfall","bungeecord"]}}}`: false,
		`{"players":"source"}`: false,
		`{}`:                   false,
		``:                     false,
	} {
		if got := playerLists([]byte(egg)); got != want {
			t.Errorf("%s: %v", egg, got)
		}
	}
	if !playerLists(eggOf([]byte(`{"image":"x","egg":{"players":"minecraft"}}`))) {
		t.Error("eggOf")
	}
}

func TestRCONPlayers(t *testing.T) {
	for egg, want := range map[string]string{
		`{"rcon":{"protocol":"webrcon","password":"RCON_PASS","players":"rust"}}`: "rust",
		`{"rcon":{"protocol":"source","password":"RCON_PASSWORD"}}`:               "",
		`{"players":"source"}`: "",
		``:                     "",
	} {
		if got := rconPlayers([]byte(egg)); got != want {
			t.Errorf("%s: %q", egg, got)
		}
	}
}

func TestResourceScan(t *testing.T) {
	for egg, want := range map[string]bool{
		`{"scan":{"rules":"fivem","dirs":["resources"]}}`: true,
		`{"scan":{}}`:             false,
		`{"players":"minecraft"}`: false,
		``:                        false,
	} {
		if got := resourceScan([]byte(egg)); got != want {
			t.Errorf("%s: %v", egg, got)
		}
	}
}
