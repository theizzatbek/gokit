package natsclient

import "encoding/json"

// Codec serializes/deserializes payloads to/from NATS message bodies.
// The default codec is JSONCodec. Override per-Client via WithCodec.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
	ContentType() string // populated into NATS header "Content-Type" on publish
}

// JSONCodec is the default Codec — uses encoding/json.
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (JSONCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func (JSONCodec) ContentType() string             { return "application/json" }

// DefaultCodec returns a fresh JSONCodec.
func DefaultCodec() Codec { return JSONCodec{} }
