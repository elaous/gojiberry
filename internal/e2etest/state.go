//go:build !(js && wasm)

package e2etest

// State is a page's #state JSON.
type State map[string]any

func (s State) Get(path string) any {
	var cur any = map[string]any(s)
	start := 0
	for i := 0; i <= len(path); i++ {
		if i < len(path) && path[i] != '.' {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[path[start:i]]
		start = i + 1
	}
	return cur
}

func (s State) Str(path string) string  { v, _ := s.Get(path).(string); return v }
func (s State) Num(path string) float64 { v, _ := s.Get(path).(float64); return v }
func (s State) Int(path string) int     { return int(s.Num(path)) }
func (s State) Bool(path string) bool   { v, _ := s.Get(path).(bool); return v }
