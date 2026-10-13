package mapinput

import (
	"reflect"
	"testing"
)

func TestRowMajorInterpretation(t *testing.T) {
	var calls []int
	grid := RowMajor{Cols: 3, Values: []int{0, 7, 8, 9, 0, 7}, Blocked: func(v int) bool { calls = append(calls, v); return v == 8 || v == 9 }}
	var got []bool
	for row := 0; row < 2; row++ {
		for col := 0; col < 3; col++ {
			got = append(got, grid.Walkable(col, row))
		}
	}
	if !reflect.DeepEqual(got, []bool{false, true, false, false, false, true}) || !reflect.DeepEqual(calls, []int{7, 8, 9, 7}) {
		t.Fatalf("got=%v calls=%v", got, calls)
	}
	grid.Values[0] = 7
	if !grid.Walkable(0, 0) {
		t.Fatal("adapter copied values")
	}
	grid.Blocked = nil
	grid.Values[0] = 0
	if grid.Walkable(0, 0) {
		t.Fatal("zero is blocked")
	}
}
