package yamlschema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/yamlschema"
)

const testSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "name": {"type": "string"},
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {"n": {"type": "integer"}}
      }
    }
  }
}`

func TestParse(t *testing.T) {
	t.Parallel()
	s, err := yamlschema.Compile("test.json", []byte(testSchema))
	require.NoError(t, err)

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "valid", in: "name: a\nitems:\n  - n: 1\n"},
		{name: "empty", in: "# only a comment\n"},
		{name: "unknown key", in: "name: a\nnope: 1\n", want: []string{"f.yaml:2:1: nope: unknown key"}},
		{
			name: "nested wrong type",
			in:   "items:\n  - n: 1\n  - n: x\n",
			want: []string{"f.yaml:3:8: items.1.n: wrong type: got string, want integer"},
		},
		{
			name: "sorted by line",
			in:   "zzz: 1\nname: 2\n",
			want: []string{"f.yaml:1:1: zzz: unknown key", "f.yaml:2:7: name: wrong type: got number, want string"},
		},
		{
			name: "syntax",
			in:   "name: [\n",
			want: []string{"f.yaml: invalid YAML: line 2: did not find expected node content"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, problems, parseErr := s.Parse("f.yaml", []byte(tt.in))
			require.NoError(t, parseErr)
			got := make([]string, 0, len(problems))
			for _, p := range problems {
				got = append(got, p.String())
			}
			if len(tt.want) == 0 {
				assert.Empty(t, got)
				require.NotNil(t, doc)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTypeAt(t *testing.T) {
	t.Parallel()
	s, err := yamlschema.Compile("test.json", []byte(testSchema))
	require.NoError(t, err)
	assert.Equal(t, "string", s.TypeAt([]string{"name"}))
	assert.Equal(t, "array", s.TypeAt([]string{"items"}))
	assert.Empty(t, s.TypeAt([]string{"missing"}))
}
