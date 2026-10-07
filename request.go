package comuse

import (
	"math"
	"strings"
	"unicode/utf8"
)

// Operation names a domain operation, never native dispatch or host authority.
type Operation string

const (
	OperationDoctor        Operation = "doctor"
	OperationState         Operation = "state"
	OperationWindows       Operation = "windows"
	OperationObserve       Operation = "a11y"
	OperationReadElement   Operation = "read_element"
	OperationWait          Operation = "wait"
	OperationLedger        Operation = "ledger"
	OperationClickElement  Operation = "click_element"
	OperationElementAction Operation = "element_action"
	OperationWriteElement  Operation = "write_element"
	OperationScrollElement Operation = "scroll_element"
	OperationClick         Operation = "click"
	OperationTypeText      Operation = "type_text"
	OperationPressKey      Operation = "press_key"
	OperationScroll        Operation = "scroll"
	OperationDrag          Operation = "drag"
	OperationFocusWindow   Operation = "focus_window"
)

// Request is a typed tagged union. Adapters decode their closed flat schemas
// and construct exactly one variant. Authority remains in trusted Config.
type Request struct {
	Operation     Operation
	Observe       *ObserveParams
	ReadElement   *ReadElementParams
	Wait          *WaitParams
	ClickElement  *ElementTarget
	ElementAction *ElementActionParams
	WriteElement  *WriteElementParams
	ScrollElement *ScrollElementParams
	Click         *ClickParams
	TypeText      *TypeTextParams
	PressKey      *PressKeyParams
	Scroll        *ScrollParams
	Drag          *DragParams
	FocusWindow   *WindowTarget
}
type ObserveParams struct {
	WindowRef string `json:"window_ref"`
	Mode      string `json:"mode,omitempty"`
	Since     string `json:"since,omitempty"`
	StateID   string `json:"state_id,omitempty"`
}
type ReadElementParams struct {
	WindowRef  string `json:"window_ref"`
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
}
type WaitParams struct {
	Condition      string `json:"condition,omitempty"`
	WindowRef      string `json:"window_ref,omitempty"`
	ElementRef     string `json:"element_ref,omitempty"`
	StateID        string `json:"state_id,omitempty"`
	ProcessRef     string `json:"process_ref,omitempty"`
	Title          string `json:"title,omitempty"`
	Expected       *bool  `json:"expected,omitempty"`
	TimeoutMS      int    `json:"timeout_ms"`
	PollIntervalMS int    `json:"poll_interval_ms,omitempty"`
}
type WindowTarget struct {
	ActionID  string `json:"action_id"`
	WindowRef string `json:"window_ref"`
}
type ElementTarget struct {
	WindowTarget
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
}
type ElementActionParams struct {
	ElementTarget
	Kind string `json:"kind"`
}
type WriteElementParams struct {
	ElementTarget
	Mode string `json:"mode"`
	Text string `json:"text"`
}
type ScrollElementParams struct {
	ElementTarget
	Direction string `json:"direction"`
	Amount    string `json:"amount"`
}
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type ClickParams struct {
	WindowTarget
	Point  Point  `json:"point"`
	Button string `json:"button"`
	Count  int    `json:"count"`
	HoldMS int    `json:"hold_ms"`
}
type TypeTextParams struct {
	WindowTarget
	Text    string `json:"text"`
	DelayMS int    `json:"delay_ms"`
}
type PressKeyParams struct {
	WindowTarget
	Keys   []string `json:"keys"`
	HoldMS int      `json:"hold_ms"`
}
type ScrollParams struct {
	WindowTarget
	Point Point `json:"point"`
	DX    int   `json:"dx"`
	DY    int   `json:"dy"`
}
type DragParams struct {
	WindowTarget
	Start      Point `json:"start"`
	End        Point `json:"end"`
	Steps      int   `json:"steps"`
	DurationMS int   `json:"duration_ms"`
}

func stateIdentity(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func validWindowTarget(p WindowTarget) bool {
	return opaqueASCII(p.ActionID) && opaqueASCII(p.WindowRef)
}
func validElementTarget(p ElementTarget) bool {
	return validWindowTarget(p.WindowTarget) && opaqueASCII(p.ElementRef) && stateIdentity(p.StateID)
}
func validPoint(p Point) bool {
	return !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0) && p.X >= 0 && p.Y >= 0 && p.X <= 1e6 && p.Y <= 1e6
}
func validKeys(keys []string) bool {
	if len(keys) < 1 || len(keys) > 8 {
		return false
	}
	seen := map[string]bool{}
	ordinary := 0
	for _, key := range keys {
		if seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "ctrl", "alt", "shift", "meta":
			continue
		case "enter", "escape", "tab", "space", "backspace", "delete", "up", "down", "left", "right", "home", "end", "page_up", "page_down":
		default:
			if len(key) != 1 || ((key[0] < 'a' || key[0] > 'z') && (key[0] < '0' || key[0] > '9')) {
				return false
			}
		}
		ordinary++
	}
	return ordinary == 1
}

// Validate rejects mixed variants and static bounds before native entry.
// Live scope, identity, focus and display containment are separately mandatory.
func (r Request) Validate() error {
	count := 0
	for _, present := range []bool{r.Observe != nil, r.ReadElement != nil, r.Wait != nil, r.ClickElement != nil, r.ElementAction != nil, r.WriteElement != nil, r.ScrollElement != nil, r.Click != nil, r.TypeText != nil, r.PressKey != nil, r.Scroll != nil, r.Drag != nil, r.FocusWindow != nil} {
		if present {
			count++
		}
	}
	valid := false
	switch r.Operation {
	case OperationDoctor, OperationState, OperationWindows, OperationLedger:
		valid = count == 0
	case OperationObserve:
		if p := r.Observe; p != nil && count == 1 {
			valid = opaqueASCII(p.WindowRef) && (p.Mode == "" || p.Mode == "auto" || p.Mode == "full" || p.Mode == "stored") && (p.Since == "" || stateIdentity(p.Since))
			if p.Mode == "stored" {
				valid = valid && p.Since == "" && stateIdentity(p.StateID)
			} else {
				valid = valid && p.StateID == ""
			}
			if p.Mode == "full" {
				valid = valid && p.Since == ""
			}
		}
	case OperationReadElement:
		if p := r.ReadElement; p != nil && count == 1 {
			valid = opaqueASCII(p.WindowRef) && opaqueASCII(p.ElementRef) && stateIdentity(p.StateID)
		}
	case OperationWait:
		if p := r.Wait; p != nil && count == 1 {
			valid = validWaitParams(*p)
		}
	case OperationClickElement:
		if p := r.ClickElement; p != nil && count == 1 {
			valid = validElementTarget(*p)
		}
	case OperationElementAction:
		if p := r.ElementAction; p != nil && count == 1 {
			valid = validElementTarget(p.ElementTarget) && (p.Kind == ActionPress || p.Kind == ActionPick || p.Kind == ActionFocus)
		}
	case OperationWriteElement:
		if p := r.WriteElement; p != nil && count == 1 {
			valid = validElementTarget(p.ElementTarget) && utf8.ValidString(p.Text) && len(p.Text) <= 8192 && (p.Mode == ActionReplace || p.Mode == ActionInsert && p.Text != "")
		}
	case OperationScrollElement:
		if p := r.ScrollElement; p != nil && count == 1 {
			valid = validElementTarget(p.ElementTarget) && (p.Direction == "up" || p.Direction == "down" || p.Direction == "left" || p.Direction == "right") && (p.Amount == "line" || p.Amount == "page")
		}
	case OperationClick:
		if p := r.Click; p != nil && count == 1 {
			valid = validWindowTarget(p.WindowTarget) && validPoint(p.Point) && (p.Button == "left" || p.Button == "right" || p.Button == "middle") && p.Count >= 1 && p.Count <= 2 && p.HoldMS >= 0 && p.HoldMS <= 1000
		}
	case OperationTypeText:
		if p := r.TypeText; p != nil && count == 1 {
			valid = validWindowTarget(p.WindowTarget) && p.Text != "" && utf8.ValidString(p.Text) && len(p.Text) <= 8192 && p.DelayMS >= 0 && p.DelayMS <= 100
		}
	case OperationPressKey:
		if p := r.PressKey; p != nil && count == 1 {
			valid = validWindowTarget(p.WindowTarget) && validKeys(p.Keys) && p.HoldMS >= 0 && p.HoldMS <= 1000
		}
	case OperationScroll:
		if p := r.Scroll; p != nil && count == 1 {
			valid = validWindowTarget(p.WindowTarget) && validPoint(p.Point) && p.DX >= -10 && p.DX <= 10 && p.DY >= -10 && p.DY <= 10 && (p.DX != 0 || p.DY != 0)
		}
	case OperationDrag:
		if p := r.Drag; p != nil && count == 1 {
			valid = validWindowTarget(p.WindowTarget) && validPoint(p.Start) && validPoint(p.End) && p.Steps >= 2 && p.Steps <= 64 && p.DurationMS >= 1 && p.DurationMS <= 2000
		}
	case OperationFocusWindow:
		if p := r.FocusWindow; p != nil && count == 1 {
			valid = validWindowTarget(*p)
		}
	}
	if !valid {
		return coreError("invalid_request")
	}
	return nil
}
func validWaitParams(p WaitParams) bool {
	if p.TimeoutMS < 1 {
		return false
	}
	if p.Condition == "" {
		return p.TimeoutMS <= 30000 && opaqueASCII(p.WindowRef) && p.ElementRef == "" && p.StateID == "" && p.ProcessRef == "" && p.Title == "" && p.Expected == nil && p.PollIntervalMS == 0
	}
	if p.TimeoutMS > 10000 || p.PollIntervalMS != 0 && (p.PollIntervalMS < 50 || p.PollIntervalMS > 1000) {
		return false
	}
	switch p.Condition {
	case "window_appears":
		return opaqueASCII(p.ProcessRef) && p.WindowRef == "" && p.ElementRef == "" && p.StateID == "" && p.Expected == nil && utf8.ValidString(p.Title) && len(p.Title) <= 256
	case "window_closed":
		return opaqueASCII(p.WindowRef) && p.ProcessRef == "" && p.Title == "" && p.ElementRef == "" && p.StateID == "" && p.Expected == nil
	case "element_exists", "element_enabled", "element_checked":
		return opaqueASCII(p.WindowRef) && opaqueASCII(p.ElementRef) && stateIdentity(p.StateID) && p.ProcessRef == "" && p.Title == "" && (p.Condition == "element_exists" && p.Expected == nil || p.Condition != "element_exists" && p.Expected != nil)
	}
	return false
}
func (r Request) mutation() bool {
	return strings.Contains("|click_element|element_action|write_element|scroll_element|click|type_text|press_key|scroll|drag|focus_window|", "|"+string(r.Operation)+"|")
}
