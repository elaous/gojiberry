package textimport

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxProblem = 200

const maxYAMLCheck = 64 << 10

func Problem(kind, src string) string {
	var err error
	switch kind {
	case JSON:
		err = checkJSON(src)
	case JSONL:
		for i, l := range strings.Split(src, "\n") {
			if strings.TrimSpace(l) != "" && !json.Valid([]byte(l)) {
				err = fmt.Errorf("line %d is not valid JSON", i+1)
				break
			}
		}
	case YAML:
		if len(src) > maxYAMLCheck {
			return ""
		}
		dec := yaml.NewDecoder(strings.NewReader(src))
		for {
			var v any
			if e := dec.Decode(&v); e != nil {
				if !errors.Is(e, io.EOF) {
					err = e
				}
				break
			}
		}
	case XML:
		err = checkXML(src)
	default:
		return ""
	}
	if err == nil {
		return ""
	}
	msg := strings.TrimPrefix(strings.Join(strings.Fields(err.Error()), " "), "yaml: ")
	if r := []rune(msg); len(r) > maxProblem {
		msg = string(r[:maxProblem]) + "…"
	}
	return msg
}

func checkJSON(src string) error {
	var v any
	err := json.Unmarshal([]byte(src), &v)
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line := 1 + strings.Count(src[:min(int(se.Offset), len(src))], "\n")
		return fmt.Errorf("line %d: %v", line, se)
	}
	return err
}

func checkXML(src string) error {
	dec := xml.NewDecoder(bytes.NewReader([]byte(src)))
	dec.Strict = true
	dec.Entity = xml.HTMLEntity
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
