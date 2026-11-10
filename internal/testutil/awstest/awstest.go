package awstest

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sassdavid/tfcensus/internal/testutil/vcr"
)

// FakeAccount replaces every account ID in recorded traffic.
const FakeAccount = "111111111111"

// Scrubber removes AWS credentials and request noise from interactions. Config, Organizations and
// other JSON protocols POST to "/", so the matcher also compares X-Amz-Target. While recording,
// account IDs become [FakeAccount].
func Scrubber() vcr.Scrubber {
	s := vcr.Scrubber{
		Headers: []string{
			"X-Amz-Security-Token", "X-Amz-Date", "Amz-Sdk-Invocation-Id", "Amz-Sdk-Request",
			"User-Agent", "X-Amz-User-Agent", "X-Amz-Sso_bearer_token",
		},
		Secrets: []string{os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")},
		Replace: []vcr.Replacement{
			{
				Pattern: regexp.MustCompile(`<(SecretAccessKey|SessionToken)>[^<]*</`),
				With:    "<$1>" + vcr.Redacted + "</",
			},
			{
				Pattern: regexp.MustCompile(`"(secretAccessKey|sessionToken|accessToken)":"[^"]*"`),
				With:    `"$1":"` + vcr.Redacted + `"`,
			},
		},
		Match: []string{"X-Amz-Target"},
	}
	if vcr.Recording() {
		s.Replace = append(s.Replace, vcr.Replacement{Pattern: regexp.MustCompile(`\b\d{12}\b`), With: FakeAccount})
	}
	return s
}

// Profile is a profile for [WriteConfig]. Without Extra it gets static fake credentials.
type Profile struct {
	Name   string
	Region string
	Extra  string // more lines for the profile section, such as sso_session = acme
}

// WriteConfig writes an AWS config file with the profiles under home/.aws and returns its path.
// extra is appended verbatim, for sso-session sections.
func WriteConfig(t testing.TB, home string, profiles []Profile, extra string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range profiles {
		fmt.Fprintf(&b, "[profile %s]\n", p.Name)
		if p.Region != "" {
			fmt.Fprintf(&b, "region = %s\n", p.Region)
		}
		if p.Extra != "" {
			b.WriteString(p.Extra + "\n")
		} else {
			b.WriteString("aws_access_key_id = AKIAFAKE\naws_secret_access_key = fake-secret\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(extra)
	path := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Replay returns a transport that answers from the cassette at path. AWS cassettes are written
// from API reference shapes and never recorded, so it only replays.
func Replay(t testing.TB, path string) http.RoundTripper {
	t.Helper()
	rec, err := vcr.New(path, vcr.Options{Scrubber: Scrubber()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rec.Stop() })
	return rec
}
