package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The balance is printed as the API wrote it. Rendering it through a float
// would round a long value, and the number on screen would stop being the
// number the account holds.
func TestWriteCredits(t *testing.T) {
	tests := []struct {
		name    string
		balance json.Number
		asJSON  bool
		want    string
	}{
		{
			name:    "one tab-separated line",
			balance: "4346.6",
			want:    "credits\t4346.6\n",
		},
		{
			name:    "a whole number stays whole",
			balance: "0",
			want:    "credits\t0\n",
		},
		{
			name:    "JSON names the value",
			balance: "4346.6",
			asJSON:  true,
			want:    "{\n  \"credits\": 4346.6\n}\n",
		},
		{
			name:    "JSON keeps a value no float could hold",
			balance: "123456789012345678",
			asJSON:  true,
			want:    "{\n  \"credits\": 123456789012345678\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeCredits(&out, tt.balance, tt.asJSON); err != nil {
				t.Fatalf("writeCredits: %v", err)
			}
			if out.String() != tt.want {
				t.Errorf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

// The JSON answer carries the balance and nothing else, so that a caller can
// read it without knowing which fields to ignore.
func TestWriteCreditsJSONHasOneField(t *testing.T) {
	var out bytes.Buffer
	if err := writeCredits(&out, "4346.6", true); err != nil {
		t.Fatalf("writeCredits: %v", err)
	}
	var got map[string]json.Number
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, out.String())
	}
	if len(got) != 1 || got["credits"] != "4346.6" {
		t.Errorf("JSON = %v, want the balance under \"credits\" alone", got)
	}
}
