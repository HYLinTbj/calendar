package model

import (
	"bytes"
	"encoding/json"
)

// Optional distinguishes "field absent" from "field explicitly null" in JSON
// update requests: an absent field leaves the current value untouched, while
// an explicit null clears it. A plain pointer can't express the difference —
// both decode to nil — so nullable update fields use this instead.
type Optional[T any] struct {
	Set   bool // the field was present in the JSON body
	Value *T   // nil when the field was null
}

func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}
