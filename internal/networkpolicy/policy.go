package networkpolicy

var AllowedOutboundOperations = []string{
	"explicit model installation",
}

// internal/asr/remote reaches another host's daemon, but only ever through a
// Unix socket that an SSH forward points at the peer — so it stays under the
// same unix-only assertion as the other two.
var AllowedNetworkPackages = map[string][]string{
	"net/http": {"internal/modelinstall"},
	"net":      {"internal/control", "internal/swayipc", "internal/asr/remote"},
}

// UnixOnlyPackages may touch net, but must never name a network other than
// "unix" when dialing or listening.
var UnixOnlyPackages = []string{"internal/control", "internal/swayipc", "internal/asr/remote"}
