package actions

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The browsers' params embed the service's types beside the database's
// own id: none of their fields may share its JSON name, or one would be
// lost.
func TestBrowserParamsDecode(t *testing.T) {
	var doc DBDocumentEditParams
	if err := json.Unmarshal([]byte(`{"id":"db1","collection":"players","op":"replace","doc_id":"7","document":"{}"}`), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != "db1" || doc.DocumentEdit.ID != "7" || doc.Collection != "players" {
		t.Errorf("document edit: %+v", doc)
	}
	var del DBDocumentEditParams
	_ = json.Unmarshal([]byte(`{"id":"db1","op":"delete","doc_ids":["7","8"]}`), &del)
	if del.ID != "db1" || len(del.IDs) != 2 {
		t.Errorf("document delete: %+v", del)
	}
	var key DBKeyEditParams
	_ = json.Unmarshal([]byte(`{"id":"db1","key":"player:alex","op":"set","value":"42"}`), &key)
	if key.ID != "db1" || key.Key != "player:alex" || key.Value != "42" {
		t.Errorf("key edit: %+v", key)
	}
	var docs DBDocumentsParams
	_ = json.Unmarshal([]byte(`{"id":"db1","collection":"players","filter":"{}","skip":50}`), &docs)
	if docs.ID != "db1" || docs.Collection != "players" || docs.Skip != 50 {
		t.Errorf("documents: %+v", docs)
	}
	for _, v := range []any{DBDocumentEditParams{}, DBKeyEditParams{}, DBKeyParams{}, DBKeysParams{}, DBDocumentsParams{}} {
		seen := map[string]bool{}
		var walk func(reflect.Type)
		walk = func(rt reflect.Type) {
			for f := range rt.Fields() {
				if f.Anonymous {
					walk(f.Type)
					continue
				}
				name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				if seen[name] {
					t.Errorf("%T: two fields are %q in JSON", v, name)
				}
				seen[name] = true
			}
		}
		walk(reflect.TypeOf(v))
	}
}
