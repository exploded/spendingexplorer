package fiscal

import "testing"

func TestApportion(t *testing.T) {
	cases := []struct {
		name       string
		value      int64
		start, end string
		want       []Share
	}{
		{"inside one year", 1000, "2026-08-01", "2026-12-31", []Share{{"2026-27", 1000}}},
		{"exactly one year", 365, "2026-07-01", "2027-06-30", []Share{{"2026-27", 365}}},
		// 2026-06-01 to 2026-07-30: 30 days in 2025-26, 30 days in 2026-27.
		{"split evenly", 600, "2026-06-01", "2026-07-30", []Share{{"2025-26", 300}, {"2026-27", 300}}},
		{"three years", 1096, "2025-07-01", "2028-06-30", []Share{{"2025-26", 365}, {"2026-27", 365}, {"2027-28", 366}}},
		{"end before start", 500, "2026-07-10", "2026-07-01", []Share{{"2026-27", 500}}},
		{"missing end", 500, "2026-07-10", "", []Share{{"2026-27", 500}}},
	}
	for _, c := range cases {
		got, err := Apportion(c.value, c.start, c.end)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestApportionSumsToValue(t *testing.T) {
	for _, v := range []int64{1, 7, 999_999, 1_234_567_891} {
		shares, err := Apportion(v, "2023-03-17", "2031-11-02")
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, s := range shares {
			sum += s.Amount
		}
		if sum != v {
			t.Errorf("value %d: shares sum to %d", v, sum)
		}
	}
}

func TestBounds(t *testing.T) {
	if f, l := Bounds("2026-27"); f != "2026-07-01" || l != "2027-06-30" {
		t.Errorf("Bounds = %s, %s", f, l)
	}
}
