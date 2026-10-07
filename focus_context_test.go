package comuse

import (
	"context"
	"testing"
)

func TestCanonicalFocusContextRequiresUniqueObservedNormalTarget(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		elements []Element
		want     string
	}{
		{"known", []Element{{Ref: "a", Role: "AXButton", Classification: "normal", Focused: &yes}}, "a"},
		{"absent", []Element{{Ref: "a", Role: "AXButton", Classification: "normal"}}, ""},
		{"false", []Element{{Ref: "a", Role: "AXButton", Classification: "normal", Focused: &no}}, ""},
		{"ambiguous", []Element{{Ref: "a", Role: "AXButton", Classification: "normal", Focused: &yes}, {Ref: "b", Role: "AXButton", Classification: "normal", Focused: &yes}}, ""},
		{"protected", []Element{{Ref: "a", Role: "AXSecureTextField", Classification: "secure", Focused: &yes}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{process: testProcess(), nativeState: "focus-state", elements: tc.elements}
			s := newTestSession(t, b, false)
			if _, err := s.Windows(context.Background()); err != nil {
				t.Fatal(err)
			}
			o, err := s.Observe(context.Background(), "window-1")
			if err != nil {
				t.Fatal(err)
			}
			full, err := projectFullObservation(s, o)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if full.Context.FocusedElementRef != nil {
				got = *full.Context.FocusedElementRef
			}
			if got != tc.want {
				t.Fatalf("focused=%q want=%q", got, tc.want)
			}
			cloned := cloneObservation(o)
			for i := range cloned.Elements {
				if cloned.Elements[i].Focused != nil {
					*cloned.Elements[i].Focused = false
				}
			}
			again, err := projectFullObservation(s, o)
			if err != nil {
				t.Fatal(err)
			}
			after := ""
			if again.Context.FocusedElementRef != nil {
				after = *again.Context.FocusedElementRef
			}
			if after != tc.want {
				t.Fatal("focus pointer alias changed retained context")
			}
		})
	}
}
