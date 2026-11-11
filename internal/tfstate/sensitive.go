package tfstate

import (
	"regexp"
	"strings"
)

// sensitiveRE matches attribute names that usually hold a secret, as whole words between
// underscores: password, master_password, client_secret, private_key_pem, auth_token.
var sensitiveRE = regexp.MustCompile(
	`(^|_)(password|passwd|passphrase|secret|token|private_key|api_key|secret_key|credentials?)(_|$)`)

// notSecretSuffixes end names that describe a secret without holding it.
func notSecretSuffixes() []string {
	return []string{
		"_arn", "_id", "_ids", "_name", "_names", "_version", "_version_id", "_length", "_enabled",
		"_policy", "_required", "_type", "_rotation", "_count", "_ttl", "_reuse_prevention",
	}
}

// SensitiveName reports whether an attribute name looks like it holds a secret, such as
// password or secret_string, and not something about one, such as kms_key_id or secret_arn.
func SensitiveName(name string) bool {
	n := strings.ToLower(name)
	if !sensitiveRE.MatchString(n) {
		return false
	}
	for _, s := range notSecretSuffixes() {
		if strings.HasSuffix(n, s) {
			return false
		}
	}
	return true
}
