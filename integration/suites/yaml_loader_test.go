package suites

import "testing"


func TestYAMLInputUserNamesResolveToConnectorIDs(t *testing.T) {
	c, err := yamlCaseToCase(yamlCase{
		Input: yamlMessage{
			User: Alice,
			Text: "ping",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Input.User != AliceID {
		t.Fatalf("input user = %q, want %q", c.Input.User, AliceID)
	}
}
