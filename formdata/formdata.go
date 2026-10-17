// Package formdata turns the answers written in a document's form fields into data to keep: a JSON object and a CSV
// row. It knows only the fields' kind, name, label and value, so any program that lists the fields can use it.
package formdata

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

// Field is a form field as far as its answer goes.
type Field struct {
	// Kind is the field's kind ("text", "number", "checkbox", ...), Name the key of its answer, Label what the person saw.
	Kind, Name, Label string
	// Value is what was written in it ("true" for a ticked checkbox).
	Value string
}

// Keys gives each field the key its answer is stored under: its Name, else its label made into letters, digits and
// underscores, else "field" and its number; a key that comes twice gets a number added so that no answer overwrites
// another.
func Keys(fields []Field) []string {
	seen := map[string]int{}
	keys := make([]string, len(fields))
	for i, f := range fields {
		k := strings.TrimSpace(f.Name)
		if k == "" {
			k = slug(f.Label)
		}
		if k == "" {
			k = "field" + strconv.Itoa(i+1)
		}
		if n := seen[k]; n > 0 {
			seen[k]++
			k += "_" + strconv.Itoa(n+1)
		} else {
			seen[k] = 1
		}
		keys[i] = k
	}
	return keys
}

func slug(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			under = false
		default:
			under = true
		}
	}
	return b.String()
}

// value is the answer as JSON takes it: a ticked box is true or false, a number written as one is a number, anything
// else is the text.
func value(f Field) any {
	switch f.Kind {
	case "checkbox":
		return f.Value == "true"
	case "number":
		t := strings.TrimSpace(f.Value)
		if _, err := strconv.ParseFloat(t, 64); err == nil && json.Valid([]byte(t)) {
			return json.Number(t) // exactly as written
		}
	}
	return f.Value
}

// JSON writes the answers as one object whose keys are in the order of the fields.
func JSON(fields []Field) ([]byte, error) {
	keys := Keys(fields)
	var b bytes.Buffer
	b.WriteString("{")
	for i, f := range fields {
		if i > 0 {
			b.WriteString(",")
		}
		k, err := json.Marshal(keys[i])
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(value(f))
		if err != nil {
			return nil, err
		}
		b.WriteString("\n  ")
		b.Write(k)
		b.WriteString(": ")
		b.Write(v)
	}
	if len(fields) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

// CSV writes the keys as a header row and the answers as the one row below. A cell that a spreadsheet would run as a
// formula (it starts with = + - @, a tab or a carriage return) gets a quote mark in front, unless it is a number.
func CSV(fields []Field) ([]byte, error) {
	keys := Keys(fields)
	row := make([]string, len(fields))
	for i, f := range fields {
		v := f.Value
		if f.Kind == "checkbox" {
			v = strconv.FormatBool(f.Value == "true")
		}
		row[i] = guard(v)
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write(keys); err != nil {
		return nil, err
	}
	if err := w.Write(row); err != nil {
		return nil, err
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func guard(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return s
		}
		return "'" + s
	}
	return s
}
