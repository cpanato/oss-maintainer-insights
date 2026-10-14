// Package schemas validates embedded, versioned publication contracts without network access.
package schemas

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed *.schema.json
var documents embed.FS
var initialize = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	names := []string{"analysis-input", "analysis-result", "population", "catalog", "repository-snapshot", "repository-history", "snapshot-index", "build-manifest"}
	for _, name := range names {
		data, err := documents.ReadFile(name + ".schema.json")
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if err := compiler.AddResource("https://cpanato.github.io/oss-maintainer-insights/schemas/"+name+".schema.json", doc); err != nil {
			return nil, err
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, name := range names {
		schema, err := compiler.Compile("https://cpanato.github.io/oss-maintainer-insights/schemas/" + name + ".schema.json")
		if err != nil {
			return nil, err
		}
		compiled[name] = schema
	}
	return compiled, nil
})

func ValidateJSON(name string, data []byte) error {
	compiled, err := initialize()
	if err != nil {
		return err
	}
	schema, ok := compiled[name]
	if !ok {
		return fmt.Errorf("unknown contract: %s", name)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid JSON document")
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("document does not conform to %s schema", name)
	}
	return nil
}
func Validate(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return ValidateJSON(name, data)
}
