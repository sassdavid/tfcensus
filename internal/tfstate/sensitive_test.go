package tfstate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/tfstate"
)

func TestSensitiveName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"password": true, "master_password": true, "secret": true, "secret_string": true,
		"client_secret": true, "private_key_pem": true, "auth_token": true, "ses_smtp_password_v4": true,
		"credentials": true, "id": false, "secret_arn": false, "kms_key_id": false, "password_length": false,
		"token_type": false, "manage_master_user_password": true, "tokenizer": false, "secretary": false,
	} {
		assert.Equal(t, want, tfstate.SensitiveName(name), name)
	}
}

// TestParseSensitive checks that set sensitive-looking attributes and sensitive_attributes paths
// are kept by name, and that no value is.
func TestParseSensitive(t *testing.T) {
	t.Parallel()

	const doc = `{"version":4,"terraform_version":"1.12.2","serial":1,"lineage":"l","resources":[
	  {"mode":"managed","type":"aws_db_instance","name":"db","provider":"provider[\"registry.terraform.io/hashicorp/aws\"]",
	   "instances":[{"attributes":{"id":"db-1","password":"VALUE-ONE","master_password":null,"manage_master_user_password":true,"auth_token":"",
	     "connection":{"token":"VALUE-TWO"},"secret_arn":"arn:aws:secretsmanager:eu-west-1:111111111111:secret:x"},
	     "sensitive_attributes":[[{"type":"get_attr","value":"connection"},{"type":"get_attr","value":"token"}],
	       [{"type":"index","value":{"value":0,"type":"number"}}], "junk"]}]}]}`
	st, err := tfstate.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	inst := st.Resources[0].Instances[0]
	assert.Equal(t, []string{"connection", "password"}, inst.Sensitive)
	assert.Equal(t, "db-1", inst.ID)
	assert.NotContains(t, strings.Join(inst.Sensitive, " "), "VALUE")
}
