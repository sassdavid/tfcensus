package sarifcheck_test

import (
	"testing"

	"github.com/sassdavid/tfcensus/internal/testutil/sarifcheck"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, log string
		valid     bool
	}{
		{
			name:  "minimal log",
			log:   `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[]}]}`,
			valid: true,
		},
		{name: "wrong version", log: `{"version":"2.0.0","runs":[]}`},
		{name: "run without tool", log: `{"version":"2.1.0","runs":[{"results":[]}]}`},
		{
			name: "unknown level",
			log: `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},` +
				`"results":[{"message":{"text":"m"},"level":"fatal"}]}]}`,
		},
		{
			name: "line before the first",
			log: `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"message":{"text":"m"},` +
				`"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a"},"region":{"startLine":0}}}]}]}]}`,
		},
		{name: "not JSON", log: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sarifcheck.Validate([]byte(tt.log))
			if tt.valid && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
		})
	}
}
