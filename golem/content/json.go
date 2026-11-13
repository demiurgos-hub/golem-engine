package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func decodeDocument(r io.Reader, limit int64, target any) error {
	if r == nil {
		return fmt.Errorf("nil reader")
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("document byte limit exceeded")
	}
	// encoding/json otherwise accepts duplicate properties using the last value.
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = scanJSON(d, 0); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func scanJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting limit exceeded")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid object key")
				}
				if seen[s] {
					return fmt.Errorf("duplicate JSON property %q", s)
				}
				seen[s] = true
				if err = scanJSON(d, depth+1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for d.More() {
				if err = scanJSON(d, depth+1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter")
		}
	}
	return nil
}
