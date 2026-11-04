package js

import (
	"slices"
	"strings"
)

// Map is a JavaScript object (or Map) of one value type: keys in the order JavaScript lists them,
// which is what decides the order a file or an answer is written in, and the order ties keep in
// a stable sort. Setting a key that is there keeps its place; deleting it and setting it again
// moves it to the end. Keys that are array indexes come first, in numeric order, as an object's
// do; a JavaScript Map keyed by text that is never an index ("owner/repo#7") reads the same.
// The zero value is an empty map.
type Map[V any] struct {
	keys   []string
	values map[string]V
}

// NewMap makes an empty map.
func NewMap[V any]() *Map[V] { return &Map[V]{keys: nil, values: map[string]V{}} }

// Get is the value at key and whether the key is there.
func (m *Map[V]) Get(key string) (V, bool) {
	value, ok := m.values[key]

	return value, ok
}

// Has reports whether key is there.
func (m *Map[V]) Has(key string) bool {
	_, ok := m.values[key]

	return ok
}

// Set puts value at key, appending the key when it is new.
func (m *Map[V]) Set(key string, value V) {
	if m.values == nil {
		m.values = map[string]V{}
	}

	_, ok := m.values[key]
	if !ok {
		m.keys = append(m.keys, key)
	}

	m.values[key] = value
}

// Delete removes key.
func (m *Map[V]) Delete(key string) {
	_, ok := m.values[key]
	if !ok {
		return
	}

	delete(m.values, key)

	m.keys = slices.DeleteFunc(m.keys, func(k string) bool { return k == key })
}

// Keys are the keys in JavaScript's order, as a copy the caller may keep while the map changes.
func (m *Map[V]) Keys() []string {
	return OrderKeys(m.keys)
}

// Len is how many keys the map holds.
func (m *Map[V]) Len() int { return len(m.keys) }

// Clear removes every key.
func (m *Map[V]) Clear() {
	m.keys = nil
	m.values = map[string]V{}
}

// MarshalJSON writes the map as a JSON object in key order, each value as Marshal writes it.
func (m *Map[V]) MarshalJSON() ([]byte, error) {
	var builder strings.Builder

	builder.WriteByte('{')

	for index, key := range m.Keys() {
		if index > 0 {
			builder.WriteByte(',')
		}

		writeString(&builder, key)
		builder.WriteByte(':')
		builder.Write(Marshal(m.values[key]))
	}

	builder.WriteByte('}')

	return []byte(builder.String()), nil
}

// OrderKeys is keys in JavaScript's own property order: array indexes first in numeric order,
// then the rest as written.
func OrderKeys(keys []string) []string {
	object := Object{keys: keys, values: nil}

	return object.ordered()
}
