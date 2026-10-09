package netcheck

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:63DD 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:6A10 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 100 0 0 10 0
   2: 0200000A:63DD 0300000A:C350 01 00000000:00000000 00:00000000 00000000  1000        0 3 1 0000000000000000 20 4 30 10 -1
`
	got, err := Parse(strings.NewReader(tcp), "tcp")
	if err != nil {
		t.Fatal(err)
	}
	// 0x63DD = 25565 on any address; 0x6A10 = 27152 on 127.0.0.1; the
	// established connection isn't listening.
	if len(got) != 2 || got[0] != (Socket{Port: 25565, Proto: "tcp"}) || got[1] != (Socket{Port: 27152, Proto: "tcp", Loopback: true}) {
		t.Errorf("tcp: %+v", got)
	}
	udp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  10: 00000000000000000000000000000000:4A3F 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 4 2 0000000000000000 0
  11: 00000000000000000000000001000000:4A40 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 5 2 0000000000000000 0
`
	got, _ = Parse(strings.NewReader(udp6), "udp")
	if len(got) != 2 || got[0] != (Socket{Port: 19007, Proto: "udp"}) || !got[1].Loopback {
		t.Errorf("udp6: %+v", got)
	}
	// Any address beats loopback for the same port.
	if d := dedupe([]Socket{{Port: 1, Proto: "tcp", Loopback: true}, {Port: 1, Proto: "tcp"}}); len(d) != 1 || d[0].Loopback {
		t.Errorf("dedupe: %+v", d)
	}
}
