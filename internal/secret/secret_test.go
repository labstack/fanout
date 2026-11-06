package secret

import (
	"strings"
	"testing"
)

func TestSecretShape(t *testing.T) {
	first, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("generated secrets must differ")
	}
	for _, prefix := range []string{"", "foi_", "foa_", "for_", "foc_"} {
		if !ValidSecret(prefix+first, prefix) {
			t.Errorf("generated secret rejected for prefix %q", prefix)
		}
	}
	for _, prefix := range []string{"", "foi_", "foa_", "for_", "foc_"} {
		for _, rest := range []string{"", first[:42], first + "A", first + "=", strings.Repeat("!", 43), strings.Repeat("A", 42) + "+", strings.Repeat("A", 42) + "/", first[:42] + "\n"} {
			if ValidSecret(prefix+rest, prefix) {
				t.Errorf("malformed secret accepted for prefix %q", prefix)
			}
		}
	}
	if ValidSecret("foa_"+first, "foi_") || ValidSecret("fo_"+strings.Repeat("a", 48), "foi_") {
		t.Fatal("wrong prefix accepted")
	}
}
