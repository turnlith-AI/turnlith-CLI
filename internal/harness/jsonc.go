package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tailscale/hujson"
)

// jsoncDoc describes the config file being edited: the client's name for
// error messages, and the $schema a freshly generated file starts with (empty
// when the client's config has none).
type jsoncDoc struct {
	label  string
	schema string
}

var errBrokenConfig = errors.New("existing opencode config has JSON syntax errors — fix it first")

func (d jsoncDoc) brokenConfig() error {
	if d.label == "opencode" {
		return errBrokenConfig
	}
	return fmt.Errorf("existing %s config has JSON syntax errors — fix it first", d.label)
}

// upsertBlockText merges block under [section, key], generating fresh
// documents (optional $schema + the section) when existing is nil.
func upsertBlockText(doc jsoncDoc, existing []byte, section, key string, block any) ([]byte, error) {
	if existing == nil {
		fresh := map[string]any{section: map[string]any{key: block}}
		if doc.schema != "" {
			fresh["$schema"] = doc.schema
		}
		out, err := json.MarshalIndent(fresh, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}

	root, obj, err := parseRootObject(doc, existing)
	if err != nil {
		return nil, err
	}
	unit := indentUnit(obj)

	// Resolve (or create) the section object, e.g. "provider" / "mcp".
	sectionIdx := findMember(obj, section)
	var sectionObj *hujson.Object
	// sectionUnit is the indentation prefix of the section's members (one
	// level below the section itself).
	sectionUnit := unit + unit
	if sectionIdx < 0 {
		sectionObj = &hujson.Object{}
		appendMember(obj, section, hujson.Value{Value: sectionObj}, "\n"+unit)
		if len(obj.Members) == 1 && len(obj.AfterExtra) == 0 {
			obj.AfterExtra = []byte("\n")
		}
	} else {
		so, ok := obj.Members[sectionIdx].Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("existing %s config: %q is not an object", doc.label, section)
		}
		sectionObj = so
		if len(sectionObj.Members) > 0 {
			sectionUnit = indentUnit(sectionObj)
		}
	}

	// Serialize the block with absolute indentation baked in: every newline
	// inside it carries the section members' prefix, so the parsed tree
	// lands with matching whitespace.
	blockJSON, err := json.MarshalIndent(block, sectionUnit, unit)
	if err != nil {
		return nil, err
	}
	blockValue, err := hujson.Parse(blockJSON)
	if err != nil {
		return nil, err
	}
	if idx := findMember(sectionObj, key); idx >= 0 {
		// Replace only the value: the member's own surrounding whitespace
		// and comments stay exactly as the user had them.
		sectionObj.Members[idx].Value.Value = blockValue.Value
	} else {
		appendMember(sectionObj, key, blockValue, "\n"+sectionUnit)
		if len(sectionObj.Members) == 1 && len(sectionObj.AfterExtra) == 0 {
			sectionObj.AfterExtra = []byte("\n" + unit)
		}
	}
	return root.Pack(), nil
}

// removeBlockText removes [section, key] and drops the emptied section.
func removeBlockText(doc jsoncDoc, existing []byte, section, key string) ([]byte, error) {
	root, obj, err := parseRootObject(doc, existing)
	if err != nil {
		return nil, err
	}
	sectionIdx := findMember(obj, section)
	if sectionIdx < 0 {
		return existing, nil
	}
	sectionObj, ok := obj.Members[sectionIdx].Value.Value.(*hujson.Object)
	if !ok || findMember(sectionObj, key) < 0 {
		return existing, nil
	}
	// RFC 6902 remove keeps hujson's comment-relocation behavior for the
	// bytes around the removed member.
	if err := root.Patch([]byte(fmt.Sprintf(
		`[{"op": "remove", "path": "/%s/%s"}]`, pointerEscape(section), pointerEscape(key)))); err != nil {
		return nil, err
	}
	if sectionIdx = findMember(obj, section); sectionIdx >= 0 {
		if so, ok := obj.Members[sectionIdx].Value.Value.(*hujson.Object); ok && len(so.Members) == 0 {
			obj.Members = append(obj.Members[:sectionIdx], obj.Members[sectionIdx+1:]...)
		}
	}
	return root.Pack(), nil
}

func hasBlock(existing []byte, section, key string) bool {
	root, err := hujson.Parse(existing)
	if err != nil {
		return false
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		return false
	}
	i := findMember(obj, section)
	if i < 0 {
		return false
	}
	so, ok := obj.Members[i].Value.Value.(*hujson.Object)
	return ok && findMember(so, key) >= 0
}

func parseRootObject(doc jsoncDoc, existing []byte) (hujson.Value, *hujson.Object, error) {
	root, err := hujson.Parse(existing)
	if err != nil {
		return root, nil, doc.brokenConfig()
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		return root, nil, fmt.Errorf("existing %s config is not a JSON object", doc.label)
	}
	return root, obj, nil
}

func findMember(obj *hujson.Object, name string) int {
	want := []byte(hujson.String(name))
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && bytes.Equal(lit, want) {
			return i
		}
	}
	return -1
}

// appendMember adds "name": value to obj with the given BeforeExtra (a
// newline plus indentation) and single spaces around the colon, matching
// conventional JSON style; it returns the member index.
func appendMember(obj *hujson.Object, name string, value hujson.Value, before string) int {
	nameValue := hujson.Value{Value: hujson.String(name)}
	nameValue.BeforeExtra = []byte(before)
	if len(value.BeforeExtra) == 0 {
		value.BeforeExtra = []byte(" ")
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{Name: nameValue, Value: value})
	return len(obj.Members) - 1
}

// indentUnit infers one nesting level of indentation from the object's
// existing members (2 spaces by default).
func indentUnit(obj *hujson.Object) string {
	for i := range obj.Members {
		w := string(obj.Members[i].Name.BeforeExtra)
		if j := strings.LastIndexByte(w, '\n'); j >= 0 {
			return w[j+1:]
		}
	}
	return "  "
}

// pointerEscape encodes a path segment per RFC 6902.
func pointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}
