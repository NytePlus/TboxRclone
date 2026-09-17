package smh

import (
	"encoding/json"
)

// Identifier preserves exact numeric API identifiers and opaque string forms.
// Do not decode numeric IDs through float64: deployed recycle IDs are integers.
type Identifier string

func (id *Identifier) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var value string
		if err := json.Unmarshal(b, &value); err != nil || value == "" {
			return ErrProtocol
		}
		*id = Identifier(value)
		return nil
	}
	if len(b) == 0 || !json.Valid(b) {
		return ErrProtocol
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return ErrProtocol
		}
	}
	*id = Identifier(b)
	return nil
}
