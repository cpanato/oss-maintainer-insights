package schemas

import (
	"os"
	"testing"
)

func TestEmbeddedContracts(t *testing.T) {
	for _, item := range []struct{ name, path string }{{"analysis-input", "../tests/fixtures/monthly-input.json"}, {"analysis-result", "../tests/fixtures/monthly-expected.json"}, {"repository-snapshot", "../tests/fixtures/live-snapshot.json"}, {"catalog", "../catalog/repositories.json"}} {
		data, err := os.ReadFile(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateJSON(item.name, data); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRejectsUnsupportedFieldsAndFormats(t *testing.T) {
	for _, value := range []string{`{}`, `{"body":"secret"}`, `{} {}`} {
		if ValidateJSON("analysis-input", []byte(value)) == nil {
			t.Fatal("invalid contract accepted")
		}
	}
	if ValidateJSON("catalog", []byte(`[{"repository":"example/repo","group":"Unknown"}]`)) == nil {
		t.Fatal("unknown group accepted")
	}
}
