package comuse

import (
	"context"
	"strings"
)

const (
	developerActionClick            = "click"
	developerActionTypeText         = "type_text"
	developerActionPressKey         = "press_key"
	developerActionCoordinateScroll = "coordinate_scroll"
	developerActionDrag             = "drag"
	developerActionFocusWindow      = "focus_window"
)

// Click submits one bounded, host-authorized coordinate click.
func (s *Session) Click(ctx context.Context, params ClickParams) (ActionResult, error) {
	return s.runDeveloperAction(ctx, Request{Operation: OperationClick, Click: &params})
}

// TypeText submits bounded UTF-8 text to the currently focused native target.
func (s *Session) TypeText(ctx context.Context, params TypeTextParams) (ActionResult, error) {
	return s.runDeveloperAction(ctx, Request{Operation: OperationTypeText, TypeText: &params})
}

// PressKey submits one closed key or modifier chord.
func (s *Session) PressKey(ctx context.Context, params PressKeyParams) (ActionResult, error) {
	return s.runDeveloperAction(ctx, Request{Operation: OperationPressKey, PressKey: &params})
}

// Scroll submits a bounded coordinate scroll.
func (s *Session) Scroll(ctx context.Context, params ScrollParams) (ActionResult, error) {
	return s.runDeveloperAction(ctx, Request{Operation: OperationScroll, Scroll: &params})
}

// Drag submits a bounded coordinate drag.
func (s *Session) Drag(ctx context.Context, params DragParams) (ActionResult, error) {
	return s.runDeveloperAction(ctx, Request{Operation: OperationDrag, Drag: &params})
}

// FocusWindow requests native focus for one in-scope window.
func (s *Session) FocusWindow(ctx context.Context, params WindowTarget) (ActionResult, error) {
	return s.runDeveloperAction(ctx, Request{Operation: OperationFocusWindow, FocusWindow: &params})
}

func (s *Session) runDeveloperAction(ctx context.Context, request Request) (ActionResult, error) {
	if ctx == nil || !requestMutationOperation(request.Operation) {
		return notApplied(requestActionID(request)), coreError("invalid_request")
	}
	if err := request.Validate(); err != nil {
		return notApplied(requestActionID(request)), err
	}
	action, err := actionFromDeveloperRequest(request)
	if err != nil {
		return notApplied(requestActionID(request)), err
	}
	return s.Do(ctx, action)
}

func actionFromDeveloperRequest(request Request) (Action, error) {
	switch request.Operation {
	case OperationClick:
		p := request.Click
		return Action{ID: p.ActionID, WindowRef: p.WindowRef, Kind: developerActionClick,
			X: p.Point.X, Y: p.Point.Y, Button: p.Button, Count: p.Count, HoldMS: p.HoldMS}, nil
	case OperationTypeText:
		p := request.TypeText
		return Action{ID: p.ActionID, WindowRef: p.WindowRef, Kind: developerActionTypeText,
			Text: p.Text, DelayMS: p.DelayMS}, nil
	case OperationPressKey:
		p := request.PressKey
		return Action{ID: p.ActionID, WindowRef: p.WindowRef, Kind: developerActionPressKey,
			Keys: canonicalKeyChord(p.Keys), HoldMS: p.HoldMS}, nil
	case OperationScroll:
		p := request.Scroll
		return Action{ID: p.ActionID, WindowRef: p.WindowRef, Kind: developerActionCoordinateScroll,
			X: p.Point.X, Y: p.Point.Y, DX: p.DX, DY: p.DY}, nil
	case OperationDrag:
		p := request.Drag
		return Action{ID: p.ActionID, WindowRef: p.WindowRef, Kind: developerActionDrag,
			X: p.Start.X, Y: p.Start.Y, EndX: p.End.X, EndY: p.End.Y,
			Steps: p.Steps, DurationMS: p.DurationMS}, nil
	case OperationFocusWindow:
		p := request.FocusWindow
		return Action{ID: p.ActionID, WindowRef: p.WindowRef, Kind: developerActionFocusWindow}, nil
	default:
		return Action{}, coreError("invalid_request")
	}
}

func isDeveloperActionKind(kind string) bool {
	switch kind {
	case developerActionClick, developerActionTypeText, developerActionPressKey, developerActionCoordinateScroll, developerActionDrag, developerActionFocusWindow:
		return true
	default:
		return false
	}
}

func canonicalKeyChord(keys []string) string {
	order := map[string]int{"ctrl": 0, "alt": 1, "shift": 2, "meta": 3}
	modifiers := make([]string, 0, len(keys)-1)
	ordinary := ""
	for _, key := range keys {
		if _, ok := order[key]; ok {
			modifiers = append(modifiers, key)
		} else {
			ordinary = key
		}
	}
	for i := 0; i < len(modifiers); i++ {
		for j := i + 1; j < len(modifiers); j++ {
			if order[modifiers[j]] < order[modifiers[i]] {
				modifiers[i], modifiers[j] = modifiers[j], modifiers[i]
			}
		}
	}
	return strings.Join(append(modifiers, ordinary), " ")
}

func validateDeveloperAction(action Action) error {
	if !opaqueASCII(action.ID) || !opaqueASCII(action.WindowRef) || action.ElementRef != "" || action.StateID != "" ||
		action.Direction != "" || action.Amount != "" {
		return coreError("invalid_request")
	}
	var request Request
	switch action.Kind {
	case developerActionClick:
		p := ClickParams{WindowTarget: WindowTarget{ActionID: action.ID, WindowRef: action.WindowRef},
			Point: Point{X: action.X, Y: action.Y}, Button: action.Button, Count: action.Count, HoldMS: action.HoldMS}
		request = Request{Operation: OperationClick, Click: &p}
	case developerActionTypeText:
		p := TypeTextParams{WindowTarget: WindowTarget{ActionID: action.ID, WindowRef: action.WindowRef}, Text: action.Text, DelayMS: action.DelayMS}
		request = Request{Operation: OperationTypeText, TypeText: &p}
	case developerActionPressKey:
		keys := strings.Split(action.Keys, " ")
		if canonicalKeyChord(keys) != action.Keys {
			return coreError("invalid_request")
		}
		p := PressKeyParams{WindowTarget: WindowTarget{ActionID: action.ID, WindowRef: action.WindowRef}, Keys: keys, HoldMS: action.HoldMS}
		request = Request{Operation: OperationPressKey, PressKey: &p}
	case developerActionCoordinateScroll:
		p := ScrollParams{WindowTarget: WindowTarget{ActionID: action.ID, WindowRef: action.WindowRef}, Point: Point{X: action.X, Y: action.Y}, DX: action.DX, DY: action.DY}
		request = Request{Operation: OperationScroll, Scroll: &p}
	case developerActionDrag:
		p := DragParams{WindowTarget: WindowTarget{ActionID: action.ID, WindowRef: action.WindowRef}, Start: Point{X: action.X, Y: action.Y}, End: Point{X: action.EndX, Y: action.EndY}, Steps: action.Steps, DurationMS: action.DurationMS}
		request = Request{Operation: OperationDrag, Drag: &p}
	case developerActionFocusWindow:
		p := WindowTarget{ActionID: action.ID, WindowRef: action.WindowRef}
		request = Request{Operation: OperationFocusWindow, FocusWindow: &p}
	default:
		return coreError("invalid_request")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	want, err := actionFromDeveloperRequest(request)
	if err != nil || want != action {
		return coreError("invalid_request")
	}
	return nil
}
