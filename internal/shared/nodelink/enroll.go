package nodelink

import (
	"encoding/json"

	"github.com/gowebpki/jcs"
)

// JoinTokenPrefix starts every join token.
const JoinTokenPrefix = "rpt_join_"

// EnrollPayload is what a node signs when it enrolls, proving it holds the
// key it's enrolling (and binding the key to this token).
func EnrollPayload(token string, publicKey []byte) ([]byte, error) {
	raw, err := json.Marshal(struct {
		Context   string `json:"context"`
		Token     string `json:"token"`
		PublicKey []byte `json:"public_key"`
	}{"raptor node enrollment v1", token, publicKey})
	if err != nil {
		return nil, err
	}
	return jcs.Transform(raw)
}
