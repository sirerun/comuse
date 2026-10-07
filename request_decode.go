package comuse

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	"github.com/sirerun/comuse/internal/jsonwire"
)

const maxRequestBytes = 32 * 1024

// DecodeRequest decodes one closed operation-specific flat argument object.
// It never accepts host authority or native-dispatch fields from the wire.
func DecodeRequest(operation Operation, raw []byte) (Request, error) {
	invalid := func() (Request, error) { return Request{}, coreError("invalid_request") }
	if len(raw) > maxRequestBytes || !utf8.Valid(raw) || !validSurrogateEscapes(raw) {
		return invalid()
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		switch operation {
		case OperationDoctor, OperationState, OperationWindows, OperationLedger:
			raw = []byte("{}")
		default:
			return invalid()
		}
	}

	allowed, required, ok := requestFields(operation)
	if !ok {
		return invalid()
	}
	var fields map[string]json.RawMessage
	if err := jsonwire.Decode(bytes.NewReader(raw), maxRequestBytes, &fields); err != nil || fields == nil {
		return invalid()
	}
	for name, value := range fields {
		if _, ok := allowed[name]; !ok || containsJSONNull(value) {
			return invalid()
		}
	}
	for _, name := range required {
		value, ok := fields[name]
		if !ok || isJSONNull(value) {
			return invalid()
		}
	}
	if !validOptionalPresence(operation, fields) {
		return invalid()
	}
	if !validPointPresence(operation, fields) || !validWaitFields(fields) {
		return invalid()
	}

	req := Request{Operation: operation}
	var err error
	switch operation {
	case OperationDoctor, OperationState, OperationWindows, OperationLedger:
		// The empty operation schema is enforced above.
	case OperationObserve:
		var p ObserveParams
		err = decodeParams(raw, &p)
		req.Observe = &p
	case OperationReadElement:
		var p ReadElementParams
		err = decodeParams(raw, &p)
		req.ReadElement = &p
	case OperationWait:
		var p WaitParams
		err = decodeParams(raw, &p)
		req.Wait = &p
	case OperationClickElement:
		var p ElementTarget
		err = decodeParams(raw, &p)
		req.ClickElement = &p
	case OperationElementAction:
		var p ElementActionParams
		err = decodeParams(raw, &p)
		req.ElementAction = &p
	case OperationWriteElement:
		var p WriteElementParams
		err = decodeParams(raw, &p)
		req.WriteElement = &p
	case OperationScrollElement:
		var p ScrollElementParams
		err = decodeParams(raw, &p)
		req.ScrollElement = &p
	case OperationClick:
		var p ClickParams
		err = decodeParams(raw, &p)
		req.Click = &p
	case OperationTypeText:
		var p TypeTextParams
		err = decodeParams(raw, &p)
		req.TypeText = &p
	case OperationPressKey:
		var p PressKeyParams
		err = decodeParams(raw, &p)
		req.PressKey = &p
	case OperationScroll:
		var p ScrollParams
		err = decodeParams(raw, &p)
		req.Scroll = &p
	case OperationDrag:
		var p DragParams
		err = decodeParams(raw, &p)
		req.Drag = &p
	case OperationFocusWindow:
		var p WindowTarget
		err = decodeParams(raw, &p)
		req.FocusWindow = &p
	default:
		return invalid()
	}
	if err != nil || req.Validate() != nil {
		return invalid()
	}
	return req, nil
}

func decodeParams(raw []byte, target any) error {
	return jsonwire.Decode(bytes.NewReader(raw), maxRequestBytes, target)
}

func requestFields(operation Operation) (map[string]struct{}, []string, bool) {
	var fields []string
	var required []string
	switch operation {
	case OperationDoctor, OperationState, OperationWindows, OperationLedger:
		fields = nil
	case OperationObserve:
		fields, required = []string{"window_ref", "mode", "since", "state_id"}, []string{"window_ref"}
	case OperationReadElement:
		fields = []string{"window_ref", "element_ref", "state_id"}
		required = fields
	case OperationWait:
		fields = []string{"condition", "window_ref", "element_ref", "state_id", "process_ref", "title", "expected", "timeout_ms", "poll_interval_ms"}
		required = []string{"timeout_ms"}
	case OperationClickElement:
		fields = []string{"action_id", "window_ref", "element_ref", "state_id"}
		required = fields
	case OperationElementAction:
		fields = []string{"action_id", "window_ref", "element_ref", "state_id", "kind"}
		required = fields
	case OperationWriteElement:
		fields = []string{"action_id", "window_ref", "element_ref", "state_id", "mode", "text"}
		required = fields
	case OperationScrollElement:
		fields = []string{"action_id", "window_ref", "element_ref", "state_id", "direction", "amount"}
		required = fields
	case OperationClick:
		fields = []string{"action_id", "window_ref", "point", "button", "count", "hold_ms"}
		required = fields
	case OperationTypeText:
		fields = []string{"action_id", "window_ref", "text", "delay_ms"}
		required = fields
	case OperationPressKey:
		fields = []string{"action_id", "window_ref", "keys", "hold_ms"}
		required = fields
	case OperationScroll:
		fields = []string{"action_id", "window_ref", "dx", "dy", "point"}
		required = fields
	case OperationDrag:
		fields = []string{"action_id", "window_ref", "start", "end", "steps", "duration_ms"}
		required = fields
	case OperationFocusWindow:
		fields = []string{"action_id", "window_ref"}
		required = fields
	default:
		return nil, nil, false
	}
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	return allowed, required, true
}

func validOptionalPresence(operation Operation, fields map[string]json.RawMessage) bool {
	if operation == OperationObserve && has(fields, "mode") {
		var value string
		if json.Unmarshal(fields["mode"], &value) != nil || value != "auto" && value != "full" && value != "stored" {
			return false
		}
	}
	if operation == OperationWriteElement && has(fields, "mode") {
		var value string
		if json.Unmarshal(fields["mode"], &value) != nil || value != "replace" && value != "insert" {
			return false
		}
	}
	for _, name := range []string{"since", "state_id"} {
		if has(fields, name) {
			var value string
			if json.Unmarshal(fields[name], &value) != nil || !stateIdentity(value) {
				return false
			}
		}
	}
	if operation == OperationWait && has(fields, "condition") {
		var value string
		if json.Unmarshal(fields["condition"], &value) != nil || value == "" {
			return false
		}
	}
	if operation == OperationWait && has(fields, "poll_interval_ms") {
		var value int
		if json.Unmarshal(fields["poll_interval_ms"], &value) != nil || value < 50 || value > 1000 {
			return false
		}
	}
	if operation == OperationWait && has(fields, "title") {
		var value string
		if json.Unmarshal(fields["title"], &value) != nil || len(value) == 0 || len(value) > 256 {
			return false
		}
	}
	if operation == OperationWait && !has(fields, "condition") && has(fields, "poll_interval_ms") {
		return false
	}
	return true
}

func validWaitFields(fields map[string]json.RawMessage) bool {
	if _, wait := fields["timeout_ms"]; !wait {
		return true
	}
	var condition string
	if raw, ok := fields["condition"]; ok && json.Unmarshal(raw, &condition) != nil {
		return false
	}
	var permitted []string
	switch condition {
	case "":
		permitted = []string{"window_ref", "timeout_ms"}
	case "window_appears":
		permitted = []string{"condition", "timeout_ms", "poll_interval_ms", "process_ref", "title"}
	case "window_closed":
		permitted = []string{"condition", "timeout_ms", "poll_interval_ms", "window_ref"}
	case "element_exists":
		permitted = []string{"condition", "timeout_ms", "poll_interval_ms", "window_ref", "element_ref", "state_id"}
	case "element_enabled", "element_checked":
		permitted = []string{"condition", "timeout_ms", "poll_interval_ms", "window_ref", "element_ref", "state_id", "expected"}
	default:
		return false
	}
	set := make(map[string]struct{}, len(permitted))
	for _, name := range permitted {
		set[name] = struct{}{}
	}
	for name := range fields {
		if _, ok := set[name]; !ok {
			return false
		}
	}
	return true
}

func validPointPresence(operation Operation, fields map[string]json.RawMessage) bool {
	var names []string
	switch operation {
	case OperationClick, OperationScroll:
		names = []string{"point"}
	case OperationDrag:
		names = []string{"start", "end"}
	default:
		return true
	}
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			return false
		}
		var point map[string]json.RawMessage
		if jsonwire.Decode(bytes.NewReader(raw), maxRequestBytes, &point) != nil || len(point) != 2 {
			return false
		}
		for _, axis := range []string{"x", "y"} {
			value, ok := point[axis]
			if !ok || containsJSONNull(value) {
				return false
			}
		}
	}
	return true
}

func has(fields map[string]json.RawMessage, key string) bool { _, ok := fields[key]; return ok }

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func containsJSONNull(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return containsNullValue(value)
}

func containsNullValue(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case []any:
		for _, item := range value {
			if containsNullValue(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if containsNullValue(item) {
				return true
			}
		}
	}
	return false
}

// validSurrogateEscapes rejects invalid UTF-16 surrogate sequences before
// encoding/json can replace them with U+FFFD and lose the original input.
func validSurrogateEscapes(raw []byte) bool {
	quoted := false
	for i := 0; i < len(raw); i++ {
		if !quoted {
			if raw[i] == '"' {
				quoted = true
			}
			continue
		}
		switch raw[i] {
		case '"':
			quoted = false
		case '\\':
			if i+1 >= len(raw) {
				return false
			}
			i++
			if raw[i] != 'u' {
				continue
			}
			if i+4 >= len(raw) {
				return false
			}
			unit, err := decodeHexUnit(raw[i+1 : i+5])
			if err != nil {
				return false
			}
			i += 4
			switch {
			case unit >= 0xD800 && unit <= 0xDBFF:
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return false
				}
				low, err := decodeHexUnit(raw[i+3 : i+7])
				if err != nil || low < 0xDC00 || low > 0xDFFF {
					return false
				}
				i += 6
			case unit >= 0xDC00 && unit <= 0xDFFF:
				return false
			}
		}
	}
	return !quoted
}

func decodeHexUnit(raw []byte) (uint16, error) {
	decoded := make([]byte, 2)
	if _, err := hex.Decode(decoded, raw); err != nil {
		return 0, err
	}
	return uint16(decoded[0])<<8 | uint16(decoded[1]), nil
}
