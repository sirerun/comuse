package comuse

import (
	"math"
	"strings"
	"testing"
)

func requestTarget() ElementTarget {
	return ElementTarget{WindowTarget: WindowTarget{ActionID: "a1", WindowRef: "w1"}, ElementRef: "e1", StateID: strings.Repeat("a", 64)}
}
func TestRequestClosedVariantAndBounds(t *testing.T) {
	target := requestTarget()
	expected := false
	good := []Request{
		{Operation: OperationDoctor}, {Operation: OperationState}, {Operation: OperationWindows}, {Operation: OperationLedger},
		{Operation: OperationObserve, Observe: &ObserveParams{WindowRef: "w1"}},
		{Operation: OperationReadElement, ReadElement: &ReadElementParams{WindowRef: "w1", ElementRef: "e1", StateID: target.StateID}},
		{Operation: OperationWait, Wait: &WaitParams{Condition: "element_checked", WindowRef: "w1", ElementRef: "e1", StateID: target.StateID, Expected: &expected, TimeoutMS: 10000}},
		{Operation: OperationClickElement, ClickElement: &target},
		{Operation: OperationElementAction, ElementAction: &ElementActionParams{ElementTarget: target, Kind: ActionPick}},
		{Operation: OperationWriteElement, WriteElement: &WriteElementParams{ElementTarget: target, Mode: ActionReplace}},
		{Operation: OperationScrollElement, ScrollElement: &ScrollElementParams{ElementTarget: target, Direction: "left", Amount: "line"}},
		{Operation: OperationClick, Click: &ClickParams{WindowTarget: target.WindowTarget, Point: Point{X: 1, Y: 2}, Button: "left", Count: 2, HoldMS: 1000}},
		{Operation: OperationTypeText, TypeText: &TypeTextParams{WindowTarget: target.WindowTarget, Text: "hello", DelayMS: 100}},
		{Operation: OperationPressKey, PressKey: &PressKeyParams{WindowTarget: target.WindowTarget, Keys: []string{"ctrl", "a"}, HoldMS: 1000}},
		{Operation: OperationScroll, Scroll: &ScrollParams{WindowTarget: target.WindowTarget, Point: Point{}, DX: -10, DY: 10}},
		{Operation: OperationDrag, Drag: &DragParams{WindowTarget: target.WindowTarget, Start: Point{}, End: Point{X: 1, Y: 2}, Steps: 64, DurationMS: 2000}},
		{Operation: OperationFocusWindow, FocusWindow: &target.WindowTarget},
	}
	for _, r := range good {
		t.Run(string(r.Operation), func(t *testing.T) {
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
			r.Wait = &WaitParams{WindowRef: "w1", TimeoutMS: 1}
			if r.Operation != OperationWait && ErrorCode(r.Validate()) != "invalid_request" {
				t.Fatal("mixed or wrong variant accepted")
			}
		})
	}
	for _, r := range []Request{{Operation: "execute"}, {Operation: OperationObserve}, {Operation: OperationDoctor, ClickElement: &target}} {
		if ErrorCode(r.Validate()) != "invalid_request" {
			t.Fatal("unknown/missing variant accepted")
		}
	}
}
func TestRequestSemanticModesAndStateIdentity(t *testing.T) {
	for _, p := range []ObserveParams{{WindowRef: "w1", Mode: "stored", StateID: strings.Repeat("a", 64)}, {WindowRef: "w1", Mode: "auto", Since: strings.Repeat("b", 64)}, {WindowRef: "w1", Mode: "full"}} {
		if err := (Request{Operation: OperationObserve, Observe: &p}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []ObserveParams{{WindowRef: "w1", Mode: "stored"}, {WindowRef: "w1", Mode: "stored", StateID: strings.Repeat("a", 64), Since: strings.Repeat("b", 64)}, {WindowRef: "w1", Mode: "full", Since: strings.Repeat("b", 64)}, {WindowRef: "w1", StateID: strings.Repeat("a", 64)}, {WindowRef: "w1", Since: "one"}, {WindowRef: "w1", Since: strings.Repeat("A", 64)}} {
		if ErrorCode((Request{Operation: OperationObserve, Observe: &p}).Validate()) != "invalid_request" {
			t.Fatal("bad observation mode accepted")
		}
	}
}
func TestRequestKeyCardinalityAndFinitePoints(t *testing.T) {
	for _, keys := range [][]string{{"a"}, {"ctrl", "shift", "a"}, {"meta", "page_down"}} {
		if !validKeys(keys) {
			t.Fatal("valid keys refused")
		}
	}
	for _, keys := range [][]string{nil, {"ctrl"}, {"a", "b"}, {"ctrl", "ctrl", "a"}, {"A"}, {"launch"}, {"é"}} {
		if validKeys(keys) {
			t.Fatal("bad key set accepted")
		}
	}
	for _, p := range []Point{{X: math.NaN()}, {Y: math.Inf(1)}, {X: -1}, {Y: 1e6 + 1}} {
		if validPoint(p) {
			t.Fatal("unsafe point accepted")
		}
	}
	target := requestTarget()
	p := TypeTextParams{WindowTarget: target.WindowTarget, Text: strings.Repeat("é", 4097)}
	if ErrorCode((Request{Operation: OperationTypeText, TypeText: &p}).Validate()) != "invalid_request" {
		t.Fatal("UTF8 byte ceiling ignored")
	}
	p.Text = string([]byte{0xff})
	if ErrorCode((Request{Operation: OperationTypeText, TypeText: &p}).Validate()) != "invalid_request" {
		t.Fatal("invalid UTF8 accepted")
	}
}
func TestRequestWaitConditionShapes(t *testing.T) {
	for _, p := range []WaitParams{{WindowRef: "w1", TimeoutMS: 30000}, {Condition: "window_appears", ProcessRef: "p1", TimeoutMS: 10000}, {Condition: "window_closed", WindowRef: "w1", TimeoutMS: 1, PollIntervalMS: 50}, {Condition: "element_exists", WindowRef: "w1", ElementRef: "e1", StateID: strings.Repeat("a", 64), TimeoutMS: 1}} {
		if !validWaitParams(p) {
			t.Fatal("valid wait refused")
		}
	}
	v := true
	for _, p := range []WaitParams{{WindowRef: "w1", TimeoutMS: 30001}, {WindowRef: "w1", TimeoutMS: 10, PollIntervalMS: 50}, {Condition: "window_appears", ProcessRef: "p1", WindowRef: "w1", TimeoutMS: 1}, {Condition: "window_closed", WindowRef: "w1", TimeoutMS: 10001}, {Condition: "window_closed", WindowRef: "w1", TimeoutMS: 1, PollIntervalMS: 49}, {Condition: "element_exists", WindowRef: "w1", ElementRef: "e1", StateID: strings.Repeat("a", 64), TimeoutMS: 1, Expected: &v}, {Condition: "pixel", WindowRef: "w1", TimeoutMS: 1}} {
		if validWaitParams(p) {
			t.Fatal("contradictory wait accepted")
		}
	}
}
