package tfstate_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const rulesState = `{"version":4,"terraform_version":"1.9.8","serial":3,"lineage":"l","resources":[
{"mode":"managed","type":"aws_security_group","name":"web","provider":"provider[\"registry.terraform.io/hashicorp/aws\"]",
 "instances":[{"attributes":{"id":"sg-web","arn":"arn:aws:ec2:eu-west-1:111111111111:security-group/sg-web",
  "description":"10.99.0.0/16 note",
  "ingress":[{"from_port":443,"to_port":443,"protocol":"tcp","cidr_blocks":["10.99.0.0/16","10.98.0.0/16"],
              "ipv6_cidr_blocks":[],"prefix_list_ids":[],"security_groups":["222222222222/sg-peer"],"self":true,"description":""}],
  "egress":[{"from_port":0,"to_port":0,"protocol":"-1","cidr_blocks":["0.0.0.0/0"],"ipv6_cidr_blocks":[],
             "prefix_list_ids":[],"security_groups":[],"self":false,"description":""}]}}]},
{"mode":"managed","type":"aws_security_group_rule","name":"db","provider":"provider[\"registry.terraform.io/hashicorp/aws\"]",
 "instances":[{"attributes":{"id":"sgrule-123","type":"ingress","security_group_id":"sg-db","protocol":"6",
  "from_port":5432,"to_port":5432,"cidr_blocks":null,"ipv6_cidr_blocks":["fd00:99::/64"],"prefix_list_ids":["pl-1"],
  "source_security_group_id":"sg-web","self":false}}]},
{"mode":"managed","type":"aws_iam_group_policy_attachment","name":"a","provider":"provider[\"registry.terraform.io/hashicorp/aws\"]",
 "instances":[{"attributes":{"id":"ops-20260101","group":"ops","policy_arn":"arn:aws:iam::aws:policy/ReadOnlyAccess"}}]},
{"mode":"managed","type":"aws_route53_record","name":"www","provider":"provider[\"registry.terraform.io/hashicorp/aws\"]",
 "instances":[{"attributes":{"id":"Z1_www_A","zone_id":"Z1","name":"www","fqdn":"www.example.com","type":"A",
  "set_identifier":"","records":["10.97.0.1"]}}]}
]}`

func TestRuleKeysAndIdentityAttrs(t *testing.T) {
	t.Parallel()

	identity := map[string]map[string]string{
		"aws_iam_group_policy_attachment": {"group": "group", "policy_arn": "policy_arn"},
		"aws_route53_record": {
			"zone_id":        "zone_id",
			"name":           "fqdn",
			"type":           "type",
			"set_identifier": "set_identifier",
		},
	}
	s, err := tfstate.ParseWith(strings.NewReader(rulesState), tfstate.ParseOptions{
		IdentityAttrs: func(tf string) map[string]string { return identity[tf] },
		RuleTuples:    true,
	})
	require.NoError(t, err)
	byType := map[string]tfstate.Instance{}
	for _, r := range s.Managed() {
		byType[r.Type] = r.Instances[0]
	}

	key := func(r tfstate.SGRule) string { return r.Key() }
	web := byType["aws_security_group"]
	assert.Equal(t, "sg-web", web.ID)
	assert.ElementsMatch(t, []string{
		key(tfstate.SGRule{GroupID: "sg-web", Protocol: "6", FromPort: 443, ToPort: 443, Source: "cidr4:10.99.0.0/16"}),
		key(
			tfstate.SGRule{
				GroupID:  "sg-web",
				Protocol: "tcp",
				FromPort: 443,
				ToPort:   443,
				Source:   "cidr4:10.98.0.0/16",
			},
		),
		key(tfstate.SGRule{GroupID: "sg-web", Protocol: "tcp", FromPort: 443, ToPort: 443, Source: "sg:sg-peer"}),
		key(tfstate.SGRule{GroupID: "sg-web", Protocol: "tcp", FromPort: 443, ToPort: 443, Source: "sg:sg-web"}),
		// AWS reports -1 ports for all-traffic rules.
		key(
			tfstate.SGRule{
				GroupID:  "sg-web",
				Egress:   true,
				Protocol: "-1",
				FromPort: -1,
				ToPort:   -1,
				Source:   "cidr4:0.0.0.0/0",
			},
		),
	}, web.RuleKeys)

	db := byType["aws_security_group_rule"]
	assert.ElementsMatch(t, []string{
		key(
			tfstate.SGRule{
				GroupID:  "sg-db",
				Protocol: "tcp",
				FromPort: 5432,
				ToPort:   5432,
				Source:   "cidr6:fd00:99::/64",
			},
		),
		key(tfstate.SGRule{GroupID: "sg-db", Protocol: "tcp", FromPort: 5432, ToPort: 5432, Source: "pl:pl-1"}),
		key(tfstate.SGRule{GroupID: "sg-db", Protocol: "tcp", FromPort: 5432, ToPort: 5432, Source: "sg:sg-web"}),
	}, db.RuleKeys)
	assert.NotEqual(
		t,
		key(tfstate.SGRule{GroupID: "sg-db", Protocol: "tcp", FromPort: 5432, ToPort: 5432, Source: "pl:pl-1"}),
		key(
			tfstate.SGRule{
				GroupID:  "sg-db",
				Egress:   true,
				Protocol: "tcp",
				FromPort: 5432,
				ToPort:   5432,
				Source:   "pl:pl-1",
			},
		),
	)

	assert.Equal(t, map[string]string{"group": "ops", "policy_arn": "arn:aws:iam::aws:policy/ReadOnlyAccess"},
		byType["aws_iam_group_policy_attachment"].IdentityAttrs)
	assert.Equal(t, map[string]string{"zone_id": "Z1", "name": "www.example.com", "type": "A"},
		byType["aws_route53_record"].IdentityAttrs)

	ix := tfstate.BuildIndex(
		[]tfstate.UnitState{{Repo: "live", Path: "net", Workspace: "default", Status: tfstate.StatusOK, State: s}},
	)
	out, err := json.Marshal(ix)
	require.NoError(t, err)
	for _, value := range []string{"10.99.0.0", "10.98.0.0", "fd00:99", "0.0.0.0/0", "5432", "10.97.0.1"} {
		assert.NotContains(t, string(out), value, "the index holds a rule or record value")
	}
}

func TestParseWithoutOptionsKeepsNoExtras(t *testing.T) {
	t.Parallel()

	s, err := tfstate.Parse(strings.NewReader(rulesState))
	require.NoError(t, err)
	for _, r := range s.Managed() {
		assert.Empty(t, r.Instances[0].RuleKeys, r.Type)
		assert.Empty(t, r.Instances[0].IdentityAttrs, r.Type)
	}
}
