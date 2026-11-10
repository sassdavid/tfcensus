package tfstate

import "strings"

// Registry hosts that serve the same provider namespaces. OpenTofu mirrors the hashicorp
// namespace, so registry.opentofu.org/hashicorp/aws is the same provider as the Terraform one.
const (
	hostTerraform = "registry.terraform.io"
	hostOpenTofu  = "registry.opentofu.org"
)

// Provider is a provider configuration address from state.
type Provider struct {
	Source string // namespace/type for the public registries, else host/namespace/type
	Alias  string
	Host   string // the registry host as written in state
}

// ParseProvider parses provider["registry.terraform.io/hashicorp/aws"].alias, and the legacy
// provider.aws.alias form of states written by Terraform 0.12.
func ParseProvider(raw string) Provider {
	if rest, ok := strings.CutPrefix(raw, `provider["`); ok {
		addr, tail, _ := strings.Cut(rest, `"]`)
		p := Provider{Alias: strings.TrimPrefix(tail, ".")}
		p.Source, p.Host = normalizeSource(addr)
		return p
	}
	if rest, ok := strings.CutPrefix(raw, "provider."); ok {
		name, alias, _ := strings.Cut(rest, ".")
		return Provider{Source: "hashicorp/" + name, Alias: alias, Host: hostTerraform}
	}
	return Provider{Source: raw}
}

// normalizeSource drops the host of the public registries, so the same provider compares equal
// whichever tool wrote the state.
func normalizeSource(addr string) (string, string) {
	parts := strings.Split(addr, "/")
	const full = 3
	if len(parts) != full {
		return addr, ""
	}
	switch parts[0] {
	case hostTerraform, hostOpenTofu:
		return parts[1] + "/" + parts[2], parts[0]
	default:
		return addr, parts[0]
	}
}

// OpenTofu reports whether the state names a provider from the OpenTofu registry, which only
// OpenTofu writes.
func (p Provider) OpenTofu() bool { return p.Host == hostOpenTofu }
