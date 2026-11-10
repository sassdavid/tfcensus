package execx

import "strings"

// Redacted replaces secret values in logged command lines.
const Redacted = "***"

// isSecretName reports whether a flag or variable name looks like it carries a secret. Matching is
// case-insensitive and ignores "_" and "-" (db_password, --api-key, AWS_SECRET_ACCESS_KEY).
func isSecretName(name string) bool {
	n := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	for _, w := range []string{
		"token", "secret", "password", "passwd", "credential",
		"privatekey", "accesskey", "sessionkey", "apikey", "auth",
	} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// Redact returns a copy of args that is safe to log: values of secret-looking flags (--token x,
// --token=x) and KEY=VALUE pairs with a secret-looking key (-var 'db_password=x') are replaced.
func Redact(args []string) []string {
	out := make([]string, len(args))
	maskNext := false
	for i, a := range args {
		if maskNext {
			out[i] = Redacted
			maskNext = false
			continue
		}
		if !strings.HasPrefix(a, "-") {
			out[i] = redactPair(a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		prefix := a[:len(a)-len(value)]
		switch {
		case isSecretName(name) && hasValue:
			out[i] = prefix + Redacted
		case isSecretName(name):
			out[i] = a
			maskNext = true
		case hasValue:
			out[i] = prefix + redactPair(value)
		default:
			out[i] = a
		}
	}
	return out
}

// redactPair masks KEY=VALUE when KEY looks secret.
func redactPair(v string) string {
	if key, _, ok := strings.Cut(v, "="); ok && isSecretName(key) {
		return key + "=" + Redacted
	}
	return v
}
