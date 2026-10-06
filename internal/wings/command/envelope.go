// Package command receives commands from the Panel and decides whether to run
// them (docs/SECURITY-MODEL.md#passkey-signed-commands):
//
//   - Every command carries the Panel's grant: an Ed25519 signature by the
//     Panel's key (pinned at enrollment) saying user U may do this exact
//     command. It's bound to the command ID, so it can't be reused.
//   - Dangerous commands also carry the user's passkey signature (a WebAuthn
//     assertion) over the exact command, from a key this node trusts. Wings
//     verifies it itself, so the Panel can't forge these commands.
//   - Every command ID runs at most once; a retry returns the stored result.
//
// The envelope itself is shared with the Panel (internal/shared/nodecmd).
package command

import "github.com/xena-studios/raptor/internal/shared/nodecmd"

// The envelope types, as the Panel sends them.
type (
	Envelope         = nodecmd.Envelope
	Grant            = nodecmd.Grant
	PasskeySignature = nodecmd.PasskeySignature
)
