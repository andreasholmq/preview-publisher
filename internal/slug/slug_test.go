package slug

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "lowercases", input: "Checkout-Redesign", want: "checkout-redesign"},
		{name: "reserved", input: "api", wantErr: true},
		{name: "leading hyphen", input: "-demo", wantErr: true},
		{name: "trailing hyphen", input: "demo-", wantErr: true},
		{name: "repeated hyphen", input: "demo--one", wantErr: true},
		{name: "invalid char", input: "demo_one", wantErr: true},
		{name: "too short", input: "ab", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateProducesValidSlug(t *testing.T) {
	got, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(got); err != nil {
		t.Fatalf("generated invalid slug %q: %v", got, err)
	}
}
