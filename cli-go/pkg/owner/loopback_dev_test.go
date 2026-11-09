package owner

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// ⭐ A dev server (`polis serve --dev`) runs as remote-client-signed and is
// still a laptop, so loopback trust holds. ⛔ Without Dev, a remote mode on
// loopback stays untrusted: that is the port-forward case the mode check
// exists for. And Dev never helps off loopback.
func TestLoopbackTrustOnADevServerAndNowhereElseRemote(t *testing.T) {
	p := LoopbackProvider{}
	for _, c := range []struct {
		inst Instance
		want bool
	}{
		{Instance{Loopback: true, Mode: mode.Local}, true},
		{Instance{Loopback: true, Mode: mode.RemoteClientSigned, Dev: true}, true},
		{Instance{Loopback: true, Mode: mode.RemoteClientSigned}, false},
		{Instance{Loopback: false, Mode: mode.RemoteClientSigned, Dev: true}, false},
		{Instance{Loopback: true, Mode: mode.Custodied}, false},
	} {
		if got := p.Available(c.inst); got != c.want {
			t.Errorf("%+v: Available = %v, want %v", c.inst, got, c.want)
		}
	}
}
