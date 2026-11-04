package server

// MarshalJSON lets the tests check the JSON text without a route that returns user text.
func MarshalJSON(v any) []byte { return marshalJSON(v) }
