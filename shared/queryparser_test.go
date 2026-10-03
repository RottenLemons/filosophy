package shared

import (
	"reflect"
	"testing"
)

func TestParseQueryFiltersAndText(t *testing.T) {
	query := ParseQuery("annual report type:doc size:>2mb -draft")

	if query.Text != "annual report" {
		t.Errorf("Text = %q, want %q", query.Text, "annual report")
	}
	if query.SizeMin != 2*1024*1024 || query.SizeMax != -1 {
		t.Errorf("size bounds = (%d, %d), want (%d, -1)", query.SizeMin, query.SizeMax, 2*1024*1024)
	}
	if !reflect.DeepEqual(query.ExcludeTerms, []string{"draft"}) {
		t.Errorf("ExcludeTerms = %v, want [draft]", query.ExcludeTerms)
	}
	for _, ext := range []string{".pdf", ".docx", ".txt"} {
		if !contains(query.ExtFilter, ext) {
			t.Errorf("ExtFilter %v does not include %s", query.ExtFilter, ext)
		}
	}
}

func TestParseQueryNaturalTypeHint(t *testing.T) {
	query := ParseQuery("holiday photos")
	if query.Text != "holiday" {
		t.Errorf("Text = %q, want holiday", query.Text)
	}
	if !contains(query.ExtFilter, ".jpg") || !contains(query.ExtFilter, ".png") {
		t.Errorf("photo extensions missing from %v", query.ExtFilter)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
