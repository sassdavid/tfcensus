package sarifcheck

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schema is src/sarif-schema-2.1.0.json of github/codeql-action v4.39.3 (MIT, see
// sarif-schema-2.1.0.LICENSE) with one fix: region's anyOf sits next to its properties, as in the
// OASIS original, because a draft 2020-12 compiler rejects it inside them. Tests use it; release
// binaries don't embed it.
//
//go:embed sarif-schema-2.1.0.json
var schema []byte

// SchemaID is the $id of the embedded schema.
const SchemaID = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json"

// Validate returns an error that lists every way data breaks the SARIF 2.1.0 schema, or nil.
// Formats such as uri and date-time are checked too, which upload-sarif only warns about.
func Validate(data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("parse sarif schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err = c.AddResource(SchemaID, doc); err != nil {
		return fmt.Errorf("add sarif schema: %w", err)
	}
	sch, err := c.Compile(SchemaID)
	if err != nil {
		return fmt.Errorf("compile sarif schema: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse sarif log: %w", err)
	}
	return sch.Validate(inst) //nolint:wrapcheck // the validation error is the message
}
