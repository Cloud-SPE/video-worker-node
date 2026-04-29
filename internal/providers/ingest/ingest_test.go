package ingest

import "testing"

func TestProtocolConstants(t *testing.T) {
	cases := []struct {
		got  Protocol
		want string
	}{
		{ProtocolRTMP, "rtmp"},
		{ProtocolSRT, "srt"},
		{ProtocolWHIP, "whip"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("Protocol(%q) = %q, want %q", c.want, string(c.got), c.want)
		}
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	if ErrStreamKeyInvalid == ErrCapacityExceeded {
		t.Error("error sentinels should be distinct")
	}
	if ErrAlreadyListening == ErrNotListening {
		t.Error("error sentinels should be distinct")
	}
}
