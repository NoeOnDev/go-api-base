package notifications

import (
	"encoding/json"
	"fmt"
	"testing"
)

// A number of the data is written in the text as it was stored, not as a float64.
func TestMessageArgsKeepNumbers(t *testing.T) {
	args := messageArgs(json.RawMessage(`{"order_id": 1234567, "big": 9007199254740993, "name": "Ana"}`))
	got := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		got[fmt.Sprint(args[i])] = fmt.Sprint(args[i+1])
	}
	want := map[string]string{"order_id": "1234567", "big": "9007199254740993", "name": "Ana"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("args = %v, want %v", got, want)
	}
	if args := messageArgs(json.RawMessage(`"not an object"`)); len(args) != 0 {
		t.Errorf("args of a value that is not an object = %v", args)
	}
}
