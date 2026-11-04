package js

import (
	"encoding"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Marshal writes a Go value as JSON.stringify writes the value it stands for: struct fields in
// their order under their json names (omitempty and "-" honoured), a nil pointer, slice or map
// as null, map keys sorted, numbers as JavaScript prints them (NaN and the infinities as null,
// where encoding/json fails), strings with only what JSON.stringify escapes escaped, and a
// value's own MarshalJSON used as it is. An *Object keeps its keys in order.
func Marshal(value any) []byte {
	var builder strings.Builder

	marshalValue(&builder, reflect.ValueOf(value), 0)

	return []byte(builder.String())
}

func marshalValue(builder *strings.Builder, value reflect.Value, depth int) {
	if depth > depthMax {
		panic("js.Marshal: a value nests deeper than depthMax; is it a cycle?")
	}

	if !value.IsValid() {
		builder.WriteString(nullText)

		return
	}

	if marshalOwn(builder, value) {
		return
	}

	switch shapeOf(value.Kind()) {
	case shapeReference:
		if value.IsNil() {
			builder.WriteString(nullText)

			return
		}

		marshalValue(builder, value.Elem(), depth+1)
	case shapeStruct:
		marshalStruct(builder, value, depth)
	case shapeList:
		marshalList(builder, value, depth)
	case shapeMap:
		marshalMap(builder, value, depth)
	default:
		marshalScalar(builder, value)
	}
}

// The shapes of value Marshal tells apart.
const (
	shapeScalar = iota
	shapeReference
	shapeStruct
	shapeList
	shapeMap
	shapeString
	shapeBool
	shapeFloat
	shapeInt
	shapeUint
)

// shapeOf is how a kind of value is written.
func shapeOf(kind reflect.Kind) int {
	shape, known := map[reflect.Kind]int{
		reflect.Pointer: shapeReference, reflect.Interface: shapeReference,
		reflect.Struct: shapeStruct, reflect.Slice: shapeList, reflect.Array: shapeList,
		reflect.Map: shapeMap, reflect.String: shapeString, reflect.Bool: shapeBool,
		reflect.Float32: shapeFloat, reflect.Float64: shapeFloat,
		reflect.Int: shapeInt, reflect.Int8: shapeInt, reflect.Int16: shapeInt,
		reflect.Int32: shapeInt, reflect.Int64: shapeInt,
		reflect.Uint: shapeUint, reflect.Uint8: shapeUint, reflect.Uint16: shapeUint,
		reflect.Uint32: shapeUint, reflect.Uint64: shapeUint,
	}[kind]
	if !known {
		return shapeScalar
	}

	return shape
}

// marshalOwn writes a value that marshals itself, and is false for one that does not.
func marshalOwn(builder *strings.Builder, value reflect.Value) bool {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
	}

	if value.Type() == reflect.TypeFor[json.RawMessage]() {
		builder.WriteString(Stringify(parsedOrNull(value.Bytes())))

		return true
	}

	marshaler, own := reflect.TypeAssert[json.Marshaler](value)
	if !own {
		return false
	}

	data, err := marshaler.MarshalJSON()
	if err != nil {
		builder.WriteString(nullText)

		return true
	}

	builder.Write(data)

	return true
}

func parsedOrNull(data []byte) any {
	parsed, err := Parse(data)
	if err != nil {
		return nil
	}

	return parsed
}

func marshalScalar(builder *strings.Builder, value reflect.Value) {
	switch shapeOf(value.Kind()) {
	case shapeString:
		writeString(builder, value.String())
	case shapeBool:
		builder.WriteString(strconv.FormatBool(value.Bool()))
	case shapeFloat:
		builder.WriteString(Number(value.Float()))
	case shapeInt:
		builder.WriteString(strconv.FormatInt(value.Int(), 10))
	case shapeUint:
		builder.WriteString(strconv.FormatUint(value.Uint(), 10))
	default:
		builder.WriteString(nullText)
	}
}

// jsonField is one struct field as encoding/json names it.
type jsonField struct {
	name      string
	omitEmpty bool
	skip      bool
}

func fieldOf(field reflect.StructField) jsonField {
	if !field.IsExported() {
		return jsonField{name: "", omitEmpty: false, skip: true}
	}

	tag := field.Tag.Get("json")
	if tag == "-" {
		return jsonField{name: "", omitEmpty: false, skip: true}
	}

	name, options, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}

	return jsonField{
		name: name, omitEmpty: slices.Contains(strings.Split(options, ","), "omitempty"),
		skip: false,
	}
}

func marshalStruct(builder *strings.Builder, value reflect.Value, depth int) {
	builder.WriteByte('{')

	written := 0
	kind := value.Type()

	for index := range kind.NumField() {
		field := fieldOf(kind.Field(index))
		if field.skip {
			continue
		}

		member := value.Field(index)
		if field.omitEmpty {
			if member.IsZero() {
				continue
			}
		}

		if written > 0 {
			builder.WriteByte(',')
		}

		writeString(builder, field.name)
		builder.WriteByte(':')
		marshalValue(builder, member, depth+1)

		written++
	}

	builder.WriteByte('}')
}

func marshalList(builder *strings.Builder, value reflect.Value, depth int) {
	if value.Kind() == reflect.Slice {
		if value.IsNil() {
			builder.WriteString(nullText)

			return
		}
	}

	builder.WriteByte('[')

	for index := range value.Len() {
		if index > 0 {
			builder.WriteByte(',')
		}

		marshalValue(builder, value.Index(index), depth+1)
	}

	builder.WriteByte(']')
}

func marshalMap(builder *strings.Builder, value reflect.Value, depth int) {
	if value.IsNil() {
		builder.WriteString(nullText)

		return
	}

	keys := make([]string, 0, value.Len())
	byName := map[string]reflect.Value{}

	for _, key := range value.MapKeys() {
		name := keyName(key)
		keys = append(keys, name)
		byName[name] = value.MapIndex(key)
	}

	slices.Sort(keys)
	builder.WriteByte('{')

	for index, name := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}

		writeString(builder, name)
		builder.WriteByte(':')
		marshalValue(builder, byName[name], depth+1)
	}

	builder.WriteByte('}')
}

func keyName(key reflect.Value) string {
	if key.Kind() == reflect.String {
		return key.String()
	}

	text, isText := reflect.TypeAssert[encoding.TextMarshaler](key)
	if isText {
		data, err := text.MarshalText()
		if err == nil {
			return string(data)
		}
	}

	return strconv.FormatInt(key.Int(), 10)
}
