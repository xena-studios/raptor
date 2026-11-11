package orgs

import (
	"testing"

	"connectrpc.com/connect"
)

func TestCommunityOffset(t *testing.T) {
	for in, want := range map[string]int32{"": 0, "0": 0, "30": 30, "2147483647": 2147483647} {
		if got, err := communityOffset(in); err != nil || got != want {
			t.Errorf("%q: %d, %v", in, got, err)
		}
	}
	// Past int32 used to wrap into a negative OFFSET, which Postgres refuses.
	for _, bad := range []string{"2147483648", "4294967326", "9223372036854775808", "-30", "x", "1.5"} {
		if _, err := communityOffset(bad); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
