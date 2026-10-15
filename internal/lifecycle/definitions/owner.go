package definitions

import (
	"encoding/json"
	"fmt"
)

// marshalDocument encodes one typed owner document with the same deterministic
// struct-ordered JSON the runtime owner uses. Normalization to the journal
// canonical form happens in NormalizePayload, never here.
func marshalDocument(document any) ([]byte, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// generatedBody reads one committed generated owner body. Generation is
// explicit and checked in; production never reads source files or a store.
func generatedBody(name string) ([]byte, error) {
	body, err := generatedBodies.ReadFile(name)
	if err != nil {
		return nil, failure("generatedBody", fmt.Sprintf("The compiled %s document is missing; regenerate the artifacts.", name), err)
	}
	return body, nil
}
