package networkpolicy

var AllowedOutboundOperations = []string{
	"explicit model installation",
}

var AllowedNetworkPackages = map[string][]string{
	"net/http": {"internal/modelinstall"},
	"net":      {"internal/control", "internal/swayipc"},
}
