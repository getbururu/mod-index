package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// ErrBroken: a file that fails its format or the list's rules. Every
// error of the strict readers wraps it.
var ErrBroken = errors.New("the file failed its check")

func broken(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBroken}, args...)...)
}

// decode reads a JSON file the way the client does: at most limit bytes,
// valid UTF-8, nesting at most MaxDepth, no key twice in an object, one
// value and nothing after it; with strict, a field its type does not
// know refuses it.
func decode(b []byte, limit int, v any, strict bool) error {
	if len(b) > limit {
		return broken("%d bytes, more than %d", len(b), limit)
	}
	if !utf8.Valid(b) {
		return broken("not UTF-8")
	}
	if err := scanJSON(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(v); err != nil {
		return broken("%v", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return broken("more after the end")
	}
	return nil
}

// scanJSON checks nesting depth and duplicate keys without decoding.
func scanJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	type frame struct {
		object bool
		keys   map[string]bool
		key    bool // the next string token is a key
	}
	var stack []*frame
	for {
		t, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return broken("%v", err)
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if k, ok := t.(string); ok && top != nil && top.object && top.key {
			if top.keys[k] {
				return broken("the key %q twice", k)
			}
			top.keys[k] = true
			top.key = false
			continue
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			if len(stack) >= MaxDepth {
				return broken("nested deeper than %d", MaxDepth)
			}
			obj := t == json.Delim('{')
			stack = append(stack, &frame{object: obj, keys: map[string]bool{}, key: obj})
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].key = true
		}
	}
}

// readJSON reads a file of the repo strictly.
func readJSON(path string, limit int, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := decode(b, limit, v, true); err != nil {
		return fmt.Errorf("%s: %w", filepath.ToSlash(path), err)
	}
	return nil
}

// encode writes v deterministically: every object's keys sorted, no HTML
// escapes, and a final newline. indent "" writes one compact line (the
// published files); "  " writes the repo's own files for review.
func encode(v any, indent string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var tree any
	if err := d.Decode(&tree); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if indent != "" {
		e.SetIndent("", indent)
	}
	if err := e.Encode(tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeJSON writes v to path as encode does, through a temporary file.
func writeJSON(path string, v any, indent string) error {
	b, err := encode(v, indent)
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

// writeFile writes b to path through a temporary file in its folder.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
