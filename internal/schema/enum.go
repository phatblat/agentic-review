package schema

import (
	"encoding/json"
	"fmt"
)

func marshalEnumString(s string) ([]byte, error) {
	return json.Marshal(s)
}

func unmarshalEnumString(data []byte) (string, error) {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	return s, nil
}

func enumError(field, value string) error {
	return fmt.Errorf("schema: invalid %s %q", field, value)
}
