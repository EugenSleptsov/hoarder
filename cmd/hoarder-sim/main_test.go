package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestScenarioIsReproducibleAndDoesNotMoveOtherItemDeadline(t *testing.T) {
	var first, second bytes.Buffer
	if err := run(&first); err != nil {
		t.Fatal(err)
	}
	if err := run(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("scenario is nondeterministic")
	}
	var result []row
	if err := json.Unmarshal(first.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 8 {
		t.Fatal(len(result))
	}
	for i := 3; i < len(result); i += 2 {
		if !result[i].Forecast.NextCheckAt.Equal(result[1].Forecast.NextCheckAt) {
			t.Fatal("other item's deadline changed")
		}
	}
}
