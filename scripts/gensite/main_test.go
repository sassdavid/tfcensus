package main

import (
	"testing"
)

func TestUnitFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		most, want int
	}{
		{0, 1}, {6, 1}, {20, 1}, {21, 5}, {100, 5}, {101, 10}, {412, 25}, {1000, 50}, {1001, 100}, {25000, 2500},
	}
	for _, tt := range tests {
		if got := unitFor(tt.most); got != tt.want {
			t.Errorf("unitFor(%d) = %d, want %d", tt.most, got, tt.want)
		}
	}
}

func TestBuildTally(t *testing.T) {
	t.Parallel()
	report := []byte(`{"data": {
		"accounts": [
			{"counts": {"managed": 4, "aws-managed": 1, "unmanaged": 2, "ignored": 9}},
			{"counts": {"managed": 2, "derived": 1}}
		],
		"regions": [{"region": "global"}, {"region": "eu-west-1"}, {"region": "us-east-1"}]
	}}`)
	got, err := buildTally(report)
	if err != nil {
		t.Fatal(err)
	}
	if got.Caption != "Sample scan: 2 accounts, 3 regions" || got.Unit != 1 {
		t.Errorf("caption %q unit %d", got.Caption, got.Unit)
	}
	want := map[string]int{
		"managed":      6,
		"aws-managed":  1,
		"other-iac":    0,
		"derived":      1,
		"unmanaged":    2,
		"undetermined": 0,
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got.Rows), len(want))
	}
	for _, r := range got.Rows {
		if r.Count != want[r.Category] {
			t.Errorf("%s = %d, want %d", r.Category, r.Count, want[r.Category])
		}
	}
	if _, err = buildTally([]byte(`{"data": {"accounts": []}}`)); err == nil {
		t.Error("a report without accounts must fail")
	}
}
