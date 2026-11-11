package tfstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SGRule is one security group rule as AWS stores it: one source per rule. Its [SGRule.Key] is
// what state and the live side compare, so neither side keeps CIDRs or ports.
type SGRule struct {
	GroupID  string
	Egress   bool
	Protocol string // a name such as tcp, a number, or -1 for all
	FromPort int
	ToPort   int
	Source   string // cidr4:<cidr>, cidr6:<cidr>, pl:<prefix list id> or sg:<group id>
}

// Key returns a hash of the normalized rule.
func (r SGRule) Key() string {
	proto := normalizeProtocol(r.Protocol)
	from, to := r.FromPort, r.ToPort
	if proto == "-1" {
		// AWS reports -1 for every port of an all-traffic rule; the provider stores 0.
		from, to = -1, -1
	}
	dir := dirIngress
	if r.Egress {
		dir = dirEgress
	}
	source := r.Source
	if group, ok := strings.CutPrefix(source, "sg:"); ok {
		// Cross-account references read account/sg-id in state.
		if _, id, found := strings.Cut(group, "/"); found {
			source = "sg:" + id
		}
	}
	s := strings.Join([]string{r.GroupID, dir, proto, strconv.Itoa(from), strconv.Itoa(to), source}, "|")
	sum := sha256.Sum256([]byte(s))
	return "sgr:" + hex.EncodeToString(sum[:16])
}

func normalizeProtocol(p string) string {
	switch strings.ToLower(p) {
	case "-1", "all", "":
		return "-1"
	case "tcp":
		return "6"
	case "udp":
		return "17"
	case "icmp":
		return "1"
	case "icmpv6":
		return "58"
	}
	return strings.ToLower(p)
}

// Rule directions, as aws_security_group_rule's type and aws_security_group's blocks name them.
const (
	dirIngress = "ingress"
	dirEgress  = "egress"
)

// Types whose rule tuples the parser expands.
const (
	typeSecurityGroup     = "aws_security_group"
	typeSecurityGroupRule = "aws_security_group_rule"
)

// ruleBlock is the rule attributes of aws_security_group_rule, and of each ingress and egress
// block of aws_security_group.
type ruleBlock struct {
	Type           string   `json:"type"`
	Protocol       string   `json:"protocol"`
	FromPort       *int     `json:"from_port"`
	ToPort         *int     `json:"to_port"`
	CIDRBlocks     []string `json:"cidr_blocks"`
	IPv6CIDRBlocks []string `json:"ipv6_cidr_blocks"`
	PrefixListIDs  []string `json:"prefix_list_ids"`
	SecurityGroups []string `json:"security_groups"`
	SourceGroup    string   `json:"source_security_group_id"`
	Self           bool     `json:"self"`
}

func (b *ruleBlock) expand(group string, egress bool) []SGRule {
	base := SGRule{GroupID: group, Egress: egress, Protocol: b.Protocol}
	if b.FromPort != nil {
		base.FromPort = *b.FromPort
	}
	if b.ToPort != nil {
		base.ToPort = *b.ToPort
	}
	var sources []string
	for _, c := range b.CIDRBlocks {
		sources = append(sources, "cidr4:"+c)
	}
	for _, c := range b.IPv6CIDRBlocks {
		sources = append(sources, "cidr6:"+c)
	}
	for _, p := range b.PrefixListIDs {
		sources = append(sources, "pl:"+p)
	}
	for _, g := range b.SecurityGroups {
		sources = append(sources, "sg:"+g)
	}
	if b.SourceGroup != "" {
		sources = append(sources, "sg:"+b.SourceGroup)
	}
	if b.Self {
		sources = append(sources, "sg:"+group)
	}
	out := make([]SGRule, 0, len(sources))
	for _, s := range sources {
		r := base
		r.Source = s
		out = append(out, r)
	}
	return out
}

// ruleCollector gathers the rule attributes of one instance while the parser streams them. It
// keeps raw values only until [ruleCollector.keys] hashes them.
type ruleCollector struct {
	tfType  string
	groupID string // security_group_id of aws_security_group_rule
	rule    ruleBlock
	ingress []ruleBlock
	egress  []ruleBlock
}

func ruleAttrs() []string {
	return []string{
		"type", "protocol", "from_port", "to_port", "cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids",
		"source_security_group_id", "self", "security_group_id", dirIngress, dirEgress,
	}
}

func (c *ruleCollector) wants(name string) bool {
	if c.tfType == typeSecurityGroup {
		return name == dirIngress || name == dirEgress
	}
	return slices.Contains(ruleAttrs(), name) && name != dirIngress && name != dirEgress
}

func (c *ruleCollector) set(name string, raw []byte) error {
	var err error
	switch name {
	case dirIngress:
		err = json.Unmarshal(raw, &c.ingress)
	case dirEgress:
		err = json.Unmarshal(raw, &c.egress)
	case "security_group_id":
		err = json.Unmarshal(raw, &c.groupID)
	default:
		// Collect the scalar attributes into one object and decode them together.
		err = json.Unmarshal([]byte(`{"`+name+`":`+string(raw)+`}`), &c.rule)
	}
	if err != nil {
		return fmt.Errorf("read %s rule attribute %s: %w", c.tfType, name, err)
	}
	return nil
}

func (c *ruleCollector) keys(id string) []string {
	var rules []SGRule
	switch c.tfType {
	case typeSecurityGroup:
		for i := range c.ingress {
			rules = append(rules, c.ingress[i].expand(id, false)...)
		}
		for i := range c.egress {
			rules = append(rules, c.egress[i].expand(id, true)...)
		}
	case typeSecurityGroupRule:
		rules = c.rule.expand(c.groupID, c.rule.Type == dirEgress)
	}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Key())
	}
	slices.Sort(out)
	return slices.Compact(out)
}
