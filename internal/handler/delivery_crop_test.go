package handler

import "testing"

// A frame is four fractions naming a part of the picture; anything else
// leaves the picture whole (theGHub1/api#591).
func TestParseDeliveryCrop(t *testing.T) {
	crop, ok := parseDeliveryCrop("0.1,0.2,0.5,0.5")
	if !ok || crop.X != 0.1 || crop.Y != 0.2 || crop.W != 0.5 || crop.H != 0.5 {
		t.Fatalf("a frame was not read: %#v %v", crop, ok)
	}
	if crop.key() != "0.1000,0.2000,0.5000,0.5000" {
		t.Fatalf("the frame keys its rendition as %q", crop.key())
	}
	for _, bad := range []string{"", "1,2,3", "0.6,0,0.6,0.5", "NaN,0,0.5,0.5", "-0.1,0,0.5,0.5", "0,0,0.01,0.5", "a,b,c,d"} {
		if _, ok := parseDeliveryCrop(bad); ok {
			t.Fatalf("%q was read as a frame", bad)
		}
	}
}
