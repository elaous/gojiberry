package formdata

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

type Field struct {
	Kind, Name, Label string
	Value             string
}

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
