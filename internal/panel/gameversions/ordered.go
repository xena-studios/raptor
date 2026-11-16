package gameversions

import (
	"bytes"
	"encoding/json"
	"errors"
)

// orderedVersions is PaperMC's {"1.21": ["1.21.4", …], …}, keeping the
// families' order (newest first), which a Go map would lose.
type orderedVersions [][]string

func (o *orderedVersions) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("versions: want an object")
	}
	for dec.More() {
		if _, err := dec.Token(); err != nil { // the family's name
			return err
		}
		var list []string
		if err := dec.Decode(&list); err != nil {
			return err
		}
		*o = append(*o, list)
	}
	_, err = dec.Token()
	return err
}
