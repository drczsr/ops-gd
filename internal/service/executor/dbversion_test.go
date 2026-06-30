package executor

import "testing"

func TestBuildPacketNameCmd(t *testing.T) {
	got := buildPacketNameCmd(10001)
	want := "cat /export/server/server_10001/packetName.txt"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
